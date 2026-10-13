# Pre-install downgrade guard (step 6, `refuseOlderRequestedRelease`)

Card: `t_92d09f78`. Operator directive (2026-10-05): **"no more downgrades"**.

## The failure this closes

The stale-pin half was fixed by `596d1fb` (the default feed release was repinned
to the newest, `v0.6.0-alpha4-pre22`). That stops the *default* from being old.
It does not stop an operator — or a stale `TOLLGATE_FEED_RELEASE_TAG`, or a feed
that has rolled back — from *asking* for a release older than the one the router
already runs.

What existed was **post-install** only: `pkgVersionVerdict` (arch.go) compares
the installed version against the requested release and flags the step `warn` /
fatal. That check fires *after* `apk`/`opkg` has already moved the router
backwards. By the time the operator sees "tollgate-wrt version mismatch", the
downgrade has happened. A router that has been moved back a release is a router
that served captive-portal traffic on the wrong build in between.

## The guard

`refuseOlderRequestedRelease(job, client, staCommitted, installed, probeErr,
requestedTag) bool` (deploy.go) runs as the **first** thing step 6 does with the
package — before the laptop-side download and before the SSH push. It is reached
through the same shared-gate shape as `refuseMissingRequestedRelease`:

```go
installedPkgVer, installedProbeErr := readInstalledPkgVersionE(client)
if refuseOlderRequestedRelease(job, client, staCommitted, installedPkgVer, installedProbeErr, feedReleaseTag) {
    return
}
```

Three outcomes:

| situation | decision |
| --- | --- |
| requested release is provably **older** than installed | **REFUSE**: log the reason, restore the pre-deploy wireless snapshot (`restoreWirelessOnFailure`), then `jobFail(6, …)` naming **both** versions |
| installed version **unreadable** (transport error) | **FAIL LOUDLY** with the real error, through `jobFailAfterRestore` — never a silent skip |
| anything else — blank router, equal version, newer requested, unorderable installed version | **pass** (the reason is logged when non-empty, so the log always says what the guard decided) |

The refusal is a step-6 terminal failure like any other, so it owes the operator
the same thing every other post-step-5 failure owes: `deploy-failure-rollback.md`'s
"a failed deploy leaves the router as found". It cannot use
`jobFailAfterRestore` directly, because it must also emit the refusal reason as
the log line — so it mirrors `refuseMissingRequestedRelease`: `restoreWirelessOnFailure`
first, then `jobFail`. The unreadable-version path has no such extra obligation
and goes through the shared `jobFailAfterRestore` exit.

## Why the comparator must be numeric, not lexical

`comparePkgVersions(a, b) (cmp int, ok bool)` (arch.go) is a **pure function** —
no router, no SSH, no globals — so the ordering table is testable directly.

The trap is the release history the feed actually publishes. A plain string
compare of the pre-number gets the answer **backwards**:

```
"pre22" < "pre9"    lexicographically   ('2' < '9')
pre22  >  pre9      numerically         (22 > 9)
```

So `v0.6.0-alpha2-pre9` vs `v0.6.0-alpha4-pre22` — and the far more likely
`...-pre21` vs `...-pre22` — would both be mis-ordered by `strings.Compare`, and
the guard would call a **downgrade an upgrade** and let it through. That is
precisely the refusal this card exists to make. The pre-number is therefore
parsed with `strconv.Atoi` and compared as an integer.

The vocabulary the feed actually uses (verified against the published tag order):

- **core**: `0.6.0`, `0.6.0.1` — compared field-wise, zero-padded, so a genuine
  point release past `0.6.0` sorts above it.
- **label**: `alpha` < `beta` < `rc`, with the label number numeric too
  (`alpha10 > alpha9`).
- **pre-number**: numeric, as above.
- **bare labels are series-finalisers**: `v0.6.0-rc1` was published *after*
  `v0.6.0-rc1-pre31`, so a bare label sorts **above** its own pre-series. The
  exception is the series *opener*: `v0.6.0-alpha2-pre` (published before
  `alpha2-pre2`) carries an empty pre-number and sorts **below** `pre2`. Both
  cases are pinned in the table with the publish dates in the test comments.
- **packaging suffix `-r1`** is ignored — the router reports
  `0.6.0_alpha4_pre22-r1` (apk db spelling) against a tag
  `v0.6.0-alpha4-pre22`; they are the same release.
- **unparseable** input (`snapshot-20261013`, `latest`, `""`) returns
  `ok == false`. `installDowngradeVerdict` then **passes** and logs why: no
  downgrade may be *proven* from a string the comparator cannot order, and a
  guard that refuses on junk would be worse than the downgrade it prevents.

`trimPkgRelease` normalises the two router spellings (`_` → `-`, strip `-rN`,
strip a leading `v`) before the fields are parsed.

## "Blank router" vs "unreadable router"

`readInstalledPkgVersion` was best-effort: it collapsed *"no tollgate-wrt
installed"* and *"the probe could not run at all"* into the same `""`. The guard
needs that distinction, so `readInstalledPkgVersionE` surfaces the error and the
`readInstalledPkgVersion` spelling is kept as a thin wrapper for the
post-install readers that do not care.

- A **blank** router (the GL-AR300M16 at `192.168.11.1`) must **pass** — there is
  nothing to downgrade.
- An **unreadable** router must **fail** — a `""` produced by a dead transport is
  a hole in the evidence, not the fact "nothing installed".

`pkgVersionFromLegs` (pure) decides which, and `probeLegTransportFailed`
classifies each probe leg. The subtlety: a leg that *ran to a nonzero exit*
(`ssh.ExitError` — `cat` on a router with no apk db, `opkg` missing on an apk
router) **proves the transport is alive**. That is a *completed* leg reporting
"nothing here", not a transport death. Only a nil client, a session-creation
failure, the bounded-run timeout, or a reset connection count as the transport
being gone.

## Tests

`downgrade_guard_test.go` — table-driven throughout:

- `TestComparePkgVersions` — the ordering table (incl. the `pre9`/`pre22` trap,
  the bare-label finaliser, the numeric label, the `-r1` suffix, junk), plus an
  **antisymmetry** check so an asymmetric "always refuse" comparator cannot pass.
- `TestInstallDowngradeVerdict` — the decision table: blank / equal / newer /
  older / unorderable.
- `TestProbeLegTransportFailed`, `TestPkgVersionFromLegs`,
  `TestReadInstalledPkgVersionENilClient` — the not-installed-vs-unreadable
  classifier.
- `TestDowngradeRefusalRestoresWirelessSnapshotBeforeFailing`,
  `TestDowngradeRefusalWithoutStaDoesNotClaimARollback`,
  `TestUnreadableInstalledVersionFailsLoudly`,
  `TestDowngradeGuardInertWhenNotOlder` — the behavioural trio.
- `TestStep6DowngradeGuardWiring` — **static** assertions over the real
  `deploy.go` (AST, same mechanism as `TestDeployFailureSitesRestoreWireless`,
  because no in-process test can drive `runDeployment` without a router): the
  gate guards a `return`; the guard runs **before** the download and the SSH
  push; the probe feeds the gate; the gate restores before it fails and
  interpolates both versions.

## Out of scope

The `/api/status` ~30 s hang on non-router hosts is a known separate defect and
is untouched.
