package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// INSTALLER — pre-install downgrade guard (card t_92d09f78, operator
// directive 2026-10-05: "no more downgrades").
//
// The post-install version assertion (pkgVersionVerdict, deploy step 6) only
// fires AFTER the package manager already moved the router backwards — the
// stale feed pin half of the directive was fixed by repinning the default
// tag (596d1fb), but nothing refuses an OLDER requested release before it is
// installed. The feed's own history is the trap: the tags
// v0.6.0-alpha2-pre9 and v0.6.0-alpha4-pre22 sort LEXICOGRAPHICALLY as
// "pre9" > "pre22" — a plain string compare would happily "upgrade" a
// pre22 router to pre9. The pre-number must be compared NUMERICALLY.
//
// Decision (this card): read the installed tollgate-wrt version BEFORE any
// package byte moves (the probe the post-install verify step already uses,
// promoted to an error-aware variant) and refuse loudly when the requested
// release is older — through the same shared exit as the existing
// step-6 refusal (restore the pre-deploy wireless snapshot, then fail the
// job naming BOTH versions). A router with no tollgate-wrt installed has
// nothing to downgrade (guard passes), an equal version is an idempotent
// redeploy (passes), and an unreadable version is a TERMINAL failure with
// the real transport error — never a silent skip. See
// docs/pre-install-downgrade-guard.md.

// ─── comparator: numeric on the pre-number, spelling-agnostic ───────────────

// TestComparePkgVersions pins the ordering the guard depends on. Cases come
// from the feed's real release history (FreedomTechFeed/packages tags) plus
// the two package spellings the router can report: the apk db spelling
// (0.6.0_alpha4_pre22-r1), the opkg spelling (same, via list-installed) and
// the GitHub fallback control-file spelling (v0.5.0).
func TestComparePkgVersions(t *testing.T) {
	cases := []struct {
		name      string
		a, b      string
		want      int
		notCompar bool // explicit opt-OUT: the comparator must refuse to order
	}{
		// THE trap from the card: pre9 vs pre22 is NUMERIC, not lexical.
		{name: "pre9 is OLDER than pre22 (lexicographic trap)", a: "v0.6.0-alpha4-pre9", b: "v0.6.0-alpha4-pre22", want: -1},
		{name: "pre21 is older than pre22", a: "v0.6.0-alpha4-pre21", b: "v0.6.0-alpha4-pre22", want: -1},
		{name: "alpha2 is older than alpha4", a: "v0.6.0-alpha2-pre9", b: "v0.6.0-alpha4-pre22", want: -1},
		{name: "equal tag spellings", a: "v0.6.0-alpha4-pre22", b: "v0.6.0-alpha4-pre22", want: 0},
		// The router reports package spellings; the guard feeds it the tag.
		{name: "equal across spellings (apk db -r1 vs tag)", a: "0.6.0_alpha4_pre22-r1", b: "v0.6.0-alpha4-pre22", want: 0},
		{name: "newer installed package spelling", a: "0.6.0_alpha4_pre23-r1", b: "v0.6.0-alpha4-pre22", want: 1},
		// Core ordering and the fallback's v-prefixed spelling.
		{name: "v0.5.0 is older than any 0.6.0", a: "v0.5.0", b: "v0.6.0-alpha1", want: -1},
		{name: "final release is newer than its prereleases", a: "0.6.0", b: "0.6.0-alpha4-pre22", want: 1},
		// Bare labels, straight from the feed's publish order: v0.6.0-rc1 was
		// published 2026-10-11 19:55, AFTER v0.6.0-rc1-pre31 (18:38) — the
		// bare label finalises its own pre-series.
		{name: "bare rc1 is newer than rc1-pre31", a: "0.6.0-rc1", b: "v0.6.0-rc1-pre31", want: 1},
		{name: "bare rc2 is newer than rc1-pre31", a: "v0.6.0-rc2", b: "v0.6.0-rc1-pre31", want: 1},
		// v0.6.0-alpha2-pre (published 09-13, before alpha2-pre2 on 09-15)
		// is the series OPENER: a bare "pre" label orders before pre2.
		{name: "bare pre opener is older than pre2", a: "v0.6.0-alpha2-pre", b: "v0.6.0-alpha2-pre2", want: -1},
		// Label vocabulary ranking (feed uses alpha then rc; beta is
		// conventional) and two-digit label numbers, numerically.
		{name: "beta ranks above alpha", a: "v0.6.0-beta1", b: "v0.6.0-alpha4", want: 1},
		{name: "label numbers are numeric (alpha10 > alpha9)", a: "v0.6.0-alpha10", b: "v0.6.0-alpha9", want: 1},
		{name: "rc ranks above alpha's pre-series", a: "0.6.0-rc1", b: "0.6.0-alpha4-pre22", want: 1},
		// Core padding: 0.6.0.1 is a real point release past 0.6.0.
		{name: "core compares field-wise, padded", a: "0.6.0.1", b: "0.6.0", want: 1},
		// The opkg/apk release suffix (-r1) is packaging, not release.
		{name: "release suffix is ignored", a: "0.5.0-r1", b: "0.5.0", want: 0},
		// Not comparable: no downgrade may be "proven" from junk.
		{name: "unparseable installed", a: "snapshot-20261013", b: "v0.6.0-alpha4-pre22", want: 0, notCompar: true},
		{name: "unparseable requested", a: "v0.6.0-alpha4-pre22", b: "latest", want: 0, notCompar: true},
		{name: "empty string", a: "", b: "v0.6.0-alpha4-pre22", want: 0, notCompar: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := comparePkgVersions(tc.a, tc.b)
			if tc.notCompar {
				if ok {
					t.Errorf("comparePkgVersions(%q, %q) = (%d, comparable), want not comparable — no downgrade may be proven from a string the comparator cannot order", tc.a, tc.b, got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("comparePkgVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
			// Antisymmetry: the mirrored pair must flip (guards against an
			// asymmetric "always refuse" comparator passing the table).
			if mirrored, okM := comparePkgVersions(tc.b, tc.a); okM && mirrored != -got {
				t.Errorf("comparePkgVersions(%q, %q) = %d, want %d (antisymmetry)", tc.b, tc.a, mirrored, -got)
			}
		})
	}
}

// ─── the guard's decision function (pure, no router) ────────────────────────

// TestInstallDowngradeVerdict pins the decision table the card demands:
// older requested → refuse naming BOTH versions; blank router → pass (there
// is nothing to downgrade); equal → pass (idempotent redeploy); newer → pass;
// a version the comparator cannot order → pass, but say so in the reason so
// the operator is not told a guard ran that did not.
func TestInstallDowngradeVerdict(t *testing.T) {
	cases := []struct {
		name         string
		installed    string
		requestedTag string
		wantRefuse   bool
		// wantReasonContains are substrings the reason must carry (all of
		// them); empty means the reason must be empty.
		wantReasonContains []string
	}{
		{
			name:      "blank router must not refuse (GL-AR300M16, no tollgate-wrt)",
			installed: "", requestedTag: "v0.6.0-alpha4-pre22",
			wantRefuse: false,
		},
		{
			name:      "requested pre9 older than installed pre22 — the lexicographic trap",
			installed: "0.6.0-alpha4-pre22", requestedTag: "v0.6.0-alpha4-pre9",
			wantRefuse:         true,
			wantReasonContains: []string{"0.6.0-alpha4-pre22", "v0.6.0-alpha4-pre9"},
		},
		{
			name:      "requested pre21 older than installed pre22",
			installed: "0.6.0-alpha4-pre22-r1", requestedTag: "v0.6.0-alpha4-pre21",
			wantRefuse:         true,
			wantReasonContains: []string{"v0.6.0-alpha4-pre21"},
		},
		{
			name:      "requested alpha2 older than installed alpha4",
			installed: "0.6.0-alpha4-pre9", requestedTag: "v0.6.0-alpha2-pre9",
			wantRefuse:         true,
			wantReasonContains: []string{"v0.6.0-alpha2-pre9"},
		},
		{
			name:      "requested older than the newest real feed release",
			installed: "0.6.0-alpha4-pre23", requestedTag: "v0.6.0-alpha4-pre22",
			wantRefuse:         true,
			wantReasonContains: []string{"0.6.0-alpha4-pre23", "v0.6.0-alpha4-pre22"},
		},
		{
			name:      "bare final installed (rc1) refuses its own pre-series",
			installed: "0.6.0-rc1", requestedTag: "v0.6.0-rc1-pre31",
			wantRefuse:         true,
			wantReasonContains: []string{"0.6.0-rc1", "v0.6.0-rc1-pre31"},
		},
		{
			name:      "equal versions allow the idempotent redeploy",
			installed: "0.6.0_alpha4_pre22-r1", requestedTag: "v0.6.0-alpha4-pre22",
			wantRefuse: false,
		},
		{
			name:      "requested newer than installed (the upgrade case)",
			installed: "v0.5.0", requestedTag: "v0.6.0-alpha4-pre22",
			wantRefuse: false,
		},
		{
			name:      "requested newer within the same series",
			installed: "0.6.0-alpha2-pre9", requestedTag: "v0.6.0-alpha4-pre22",
			wantRefuse: false,
		},
		{
			name:      "unorderable installed version proceeds, saying why",
			installed: "snapshot-20261013", requestedTag: "v0.6.0-alpha4-pre22",
			wantRefuse:         false,
			wantReasonContains: []string{"snapshot-20261013", "v0.6.0-alpha4-pre22"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refuse, reason := installDowngradeVerdict(tc.installed, tc.requestedTag)
			if refuse != tc.wantRefuse {
				t.Errorf("installDowngradeVerdict(%q, %q) refuse = %v, want %v (reason: %q)",
					tc.installed, tc.requestedTag, refuse, tc.wantRefuse, reason)
			}
			if len(tc.wantReasonContains) == 0 {
				if reason != "" {
					t.Errorf("installDowngradeVerdict(%q, %q) reason = %q, want empty on a pass with nothing to explain", tc.installed, tc.requestedTag, reason)
				}
				return
			}
			for _, want := range tc.wantReasonContains {
				if !strings.Contains(reason, want) {
					t.Errorf("installDowngradeVerdict(%q, %q) reason %q does not name %q — the operator must see BOTH versions", tc.installed, tc.requestedTag, reason, want)
				}
			}
		})
	}
}

// ─── probe classification: unreadable is a transport death, not a pass ──────

// TestProbeLegTransportFailed pins the classifier that keeps the guard honest
// about WHY a version read failed: a command that RAN to a nonzero exit
// (ssh.ExitError — `cat` on an opkg router with no apk db, `opkg` missing on
// an apk router) proves the transport is alive, while no-client, session
// failures and the runBounded timeout are transport deaths.
func TestProbeLegTransportFailed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not a failure", err: nil, want: false},
		{name: "exit status (command ran, transport alive)", err: &ssh.ExitError{}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeLegTransportFailed(tc.err); got != tc.want {
				t.Errorf("probeLegTransportFailed(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}

	// A %w-wrapped ExitError still proves the command RAN — errors.As must
	// see through the chain.
	wrapped := fmt.Errorf("session setup: %w", &ssh.ExitError{})
	if got := probeLegTransportFailed(wrapped); got {
		t.Errorf("probeLegTransportFailed(%v) = true, want false: an exit status proves the command RAN — errors.As must see through the %%w chain", wrapped)
	}
	if got := probeLegTransportFailed(sshCommandTimeoutError("opkg list-installed tollgate-wrt", time.Second)); !got {
		t.Errorf("probeLegTransportFailed(command timeout) = false, want true: a router that stops answering is a transport death, not a pass")
	}
	if got := probeLegTransportFailed(errors.New("no SSH client — the router connection was lost")); !got {
		t.Errorf("probeLegTransportFailed(nil-client error) = false, want true")
	}
	if got := probeLegTransportFailed(errors.New("ssh: connection lost")); !got {
		t.Errorf("probeLegTransportFailed(connection lost) = false, want true")
	}
}

// TestPkgVersionFromLegs pins the probe's three-way classification using
// recorded router output, no router involved:
//   - a version found on either leg wins;
//   - no version anywhere with at least one leg completing on a LIVE
//     transport means "not installed" (the blank-router case — guard passes);
//   - every leg dying on the transport means UNREADABLE — the error is
//     returned so the guard can fail the step with the real reason.
func TestPkgVersionFromLegs(t *testing.T) {
	const apkDBWithTollgate = "P:tollgate-wrt\nV:0.6.0_alpha4_pre22-r1\n\nP:busybox\nV:1.36.1\n"
	const apkDBWithoutTollgate = "P:busybox\nV:1.36.1\n"
	ranCat := func() error { return &ssh.ExitError{} }  // cat: no apk db on an opkg router
	ranOpkg := func() error { return &ssh.ExitError{} } // opkg: not found on an apk router
	transport := func() error {
		return sshCommandTimeoutError("cat /lib/apk/db/installed", time.Second)
	}

	cases := []struct {
		name        string
		dbOut       string
		dbErr       error
		opkgOut     string
		opkgErr     error
		wantVersion string
		wantErr     bool
	}{
		{
			name:        "apk db answers",
			dbOut:       apkDBWithTollgate,
			opkgErr:     ranOpkg(),
			wantVersion: "0.6.0_alpha4_pre22-r1",
		},
		{
			name:        "opkg answers",
			dbErr:       ranCat(),
			opkgOut:     "tollgate-wrt - 0.6.0_alpha2_pre9-r1",
			wantVersion: "0.6.0_alpha2_pre9-r1",
		},
		{
			// The blank router from the card (GL-AR300M16: opkg, no
			// tollgate-wrt): cat fails with an exit status (alive), opkg
			// answers with nothing.
			name:        "not installed on a live opkg router",
			dbErr:       ranCat(),
			wantVersion: "",
		},
		{
			name:        "not installed on a live apk router",
			dbOut:       apkDBWithoutTollgate,
			opkgErr:     ranOpkg(),
			wantVersion: "",
		},
		{
			name:        "one live leg is enough to say not installed",
			dbErr:       transport(),
			opkgErr:     nil,
			wantVersion: "",
		},
		{
			name:    "every leg died on the transport",
			dbErr:   transport(),
			opkgErr: transport(),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := pkgVersionFromLegs(tc.dbOut, tc.dbErr, tc.opkgOut, tc.opkgErr)
			if v != tc.wantVersion {
				t.Errorf("pkgVersionFromLegs version = %q, want %q", v, tc.wantVersion)
			}
			if tc.wantErr && err == nil {
				t.Errorf("pkgVersionFromLegs err = nil, want the transport error — an unreadable version must NOT read as \"not installed\"")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("pkgVersionFromLegs err = %v, want nil (a live leg proved the router answerable)", err)
			}
			if tc.wantErr && err != nil && err.Error() != tc.dbErr.Error() {
				t.Errorf("pkgVersionFromLegs err = %q, want the real transport error %q carried verbatim", err.Error(), tc.dbErr.Error())
			}
		})
	}
}

// TestReadInstalledPkgVersionENilClient: with no client at all the probe must
// report the transport error (not \"\"), while the best-effort wrapper
// readInstalledPkgVersion keeps its \"\" contract for the post-install reads.
func TestReadInstalledPkgVersionENilClient(t *testing.T) {
	v, err := readInstalledPkgVersionE(nil)
	if v != "" || err == nil {
		t.Fatalf("readInstalledPkgVersionE(nil) = (%q, %v), want (\"\", error): a nil client is a transport death, not a blank router", v, err)
	}
	if !strings.Contains(err.Error(), "no SSH client") {
		t.Errorf("readInstalledPkgVersionE(nil) err = %q, want the sshRunE nil-client reason carried verbatim", err.Error())
	}
	if got := readInstalledPkgVersion(nil); got != "" {
		t.Errorf("readInstalledPkgVersion(nil) = %q, want \"\" (best-effort wrapper unchanged for the post-install reads)", got)
	}
}

// ─── the gate: refusal, restore-before-fail, and inertness ──────────────────

// TestDowngradeRefusalRestoresWirelessSnapshotBeforeFailing drives the gate
// with STA committed and a requested release that is provably older — the
// exact sequence the guard exists for — and asserts the same contract the
// missing-release refusal is pinned to: the pre-deploy wireless snapshot is
// restored BEFORE the job is marked failed, and the operator-facing failure
// names BOTH versions.
func TestDowngradeRefusalRestoresWirelessSnapshotBeforeFailing(t *testing.T) {
	job := newJob("192.168.1.1")
	client := new(ssh.Client)
	calls := stubWirelessRollback(t, job)

	const installedNewer = "0.6.0-alpha4-pre23"
	if stop := refuseOlderRequestedRelease(job, client, true, installedNewer, nil, feedReleaseTag); !stop {
		t.Fatal("refuseOlderRequestedRelease(...) = false, want true: the caller must stop instead of installing the older package")
	}
	if len(*calls) != 1 {
		t.Fatalf("refusal made %d wireless rollback call(s), want exactly 1 — the refusal must restore the pre-deploy snapshot like every other step-6 terminal failure", len(*calls))
	}
	if got := (*calls)[0].client; got != client {
		t.Errorf("rollback was handed %p, want the live deploy client %p", got, client)
	}
	if got := (*calls)[0].jobStatus; got != "running" {
		t.Errorf("rollback ran while the job was already %q; it must run BEFORE the job is marked failed", got)
	}

	if job.Status != "failed" {
		t.Errorf("job.Status = %q, want \"failed\"", job.Status)
	}
	if job.Steps[6].Status != "failed" {
		t.Errorf("step 6 status = %q, want \"failed\"", job.Steps[6].Status)
	}
	if !strings.Contains(job.Steps[6].Detail, installedNewer) || !strings.Contains(job.Steps[6].Detail, feedReleaseTag) {
		t.Errorf("step 6 detail does not name both versions (installed %q, requested %q): %q", installedNewer, feedReleaseTag, job.Steps[6].Detail)
	}
	if !strings.Contains(job.Error, installedNewer) || !strings.Contains(job.Error, feedReleaseTag) {
		t.Errorf("job.Error does not name both versions:\n%s", job.Error)
	}

	log := jobLogText(job)
	if !strings.Contains(log, "ERROR: ") {
		t.Errorf("deploy log does not carry the refusal as an ERROR line:\n%s", log)
	}
	rollbackAt := strings.Index(log, "Rolling back wireless config")
	if rollbackAt < 0 {
		t.Errorf("deploy log does not say the wireless config was restored:\n%s", log)
	}
	if errAt := strings.Index(log, "ERROR: "); errAt < 0 || rollbackAt < errAt {
		t.Errorf("expected the refusal to be logged before the rollback line (errAt=%d rollbackAt=%d):\n%s", errAt, rollbackAt, log)
	}
}

// TestDowngradeRefusalWithoutStaDoesNotClaimARollback: WAN-mode deploys
// committed no radio config, so the refusal must not call the rollback and
// must not tell the operator it did — while still failing loudly.
func TestDowngradeRefusalWithoutStaDoesNotClaimARollback(t *testing.T) {
	job := newJob("192.168.1.1")
	calls := stubWirelessRollback(t, job)

	if stop := refuseOlderRequestedRelease(job, new(ssh.Client), false, "0.6.0-alpha4-pre23", nil, feedReleaseTag); !stop {
		t.Fatal("refuseOlderRequestedRelease(...) = false, want true: the refusal is unchanged by the rollback decision")
	}
	if len(*calls) != 0 {
		t.Errorf("refusal called the wireless rollback %d time(s) on a deploy that committed no STA config", len(*calls))
	}
	log := jobLogText(job)
	if strings.Contains(log, "Rolling back wireless config") {
		t.Errorf("deploy log claims a wireless rollback that did not happen:\n%s", log)
	}
	if job.Status != "failed" || !strings.Contains(log, "ERROR: ") {
		t.Errorf("the refusal must fail the job loudly whether or not STA was committed (status=%q):\n%s", job.Status, log)
	}
}

// TestDowngradeGuardInertWhenNotOlder pins the pass cases: an equal version
// (idempotent redeploy), a blank router (nothing to downgrade) and an older
// installed version (the upgrade case) must all continue, touching nothing
// but the log.
func TestDowngradeGuardInertWhenNotOlder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed string
	}{
		{"equal installed version (idempotent redeploy)", "0.6.0_alpha4_pre22-r1"},
		{"older installed version (the upgrade case)", "v0.5.0"},
		{"blank router (nothing to downgrade)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := newJob("192.168.1.1")
			calls := stubWirelessRollback(t, job)

			if stop := refuseOlderRequestedRelease(job, new(ssh.Client), true, tc.installed, nil, feedReleaseTag); stop {
				t.Error("gate fired on a path that must continue: it would fail a deploy that is not a downgrade")
			}
			if len(*calls) != 0 {
				t.Errorf("no-op gate call still rolled back wireless %d time(s)", len(*calls))
			}
			if job.Status != "running" {
				t.Errorf("job.Status = %q, want \"running\" (the deploy continues)", job.Status)
			}
			log := jobLogText(job)
			if strings.Contains(log, "ERROR: ") || strings.Contains(log, "Rolling back wireless config") {
				t.Errorf("no-op gate call wrote failure text into the log:\n%s", log)
			}
		})
	}
}

// TestUnreadableInstalledVersionFailsLoudly pins the card's edge case: a
// version probe that died on the transport must NOT silently skip the guard —
// the step fails with the REAL error, through the shared exit (restore first).
func TestUnreadableInstalledVersionFailsLoudly(t *testing.T) {
	job := newJob("192.168.1.1")
	client := new(ssh.Client)
	calls := stubWirelessRollback(t, job)

	probeErr := sshCommandTimeoutError("opkg list-installed tollgate-wrt", 30*time.Second)
	if stop := refuseOlderRequestedRelease(job, client, true, "", probeErr, feedReleaseTag); !stop {
		t.Fatal("refuseOlderRequestedRelease(probe error) = false, want true: an unreadable version must not silently skip the guard")
	}
	if len(*calls) != 1 {
		t.Fatalf("unreadable-version failure made %d wireless rollback call(s), want 1 — it is a post-step-5 terminal failure like any other", len(*calls))
	}
	if got := (*calls)[0].jobStatus; got != "running" {
		t.Errorf("rollback ran while the job was already %q; it must run BEFORE the job is marked failed", got)
	}
	if job.Status != "failed" || job.Steps[6].Status != "failed" {
		t.Errorf("job.Status = %q, step 6 = %q, want both \"failed\"", job.Status, job.Steps[6].Status)
	}
	if !strings.Contains(job.Error, probeErr.Error()) {
		t.Errorf("job.Error does not carry the real transport error:\n%s", job.Error)
	}
	if !strings.Contains(job.Steps[6].Detail, "could not read the installed tollgate-wrt version") {
		t.Errorf("step 6 detail %q does not say the installed version could not be read", job.Steps[6].Detail)
	}
}

// TestUnparseableInstalledVersionLogsAndProceeds: a probe that answered with
// a string the comparator cannot order is not PROOF of a downgrade — the
// guard says so in the log and lets the deploy continue (the post-install
// verify step still has its own say).
func TestUnparseableInstalledVersionLogsAndProceeds(t *testing.T) {
	job := newJob("192.168.1.1")
	calls := stubWirelessRollback(t, job)

	if stop := refuseOlderRequestedRelease(job, new(ssh.Client), true, "snapshot-20261013", nil, feedReleaseTag); stop {
		t.Fatal("refuseOlderRequestedRelease(unorderable version) = true, want false: an unorderable version is not a proven downgrade")
	}
	if len(*calls) != 0 {
		t.Errorf("unorderable version still rolled back wireless %d time(s)", len(*calls))
	}
	if job.Status != "running" {
		t.Errorf("job.Status = %q, want \"running\"", job.Status)
	}
	if log := jobLogText(job); !strings.Contains(log, "snapshot-20261013") {
		t.Errorf("deploy log does not tell the operator the guard could not order the installed version:\n%s", log)
	}
}

// ─── static wiring guard (same spirit as TestDeployFailureSitesRestoreWireless) ──

// TestStep6DowngradeGuardWiring pins the PRE half of the card's requirement
// on the real deploy.go source: no in-process test can drive runDeployment
// without a router, so the wiring is asserted statically —
//
//  1. runDeployment calls refuseOlderRequestedRelease as the condition of an
//     if whose body returns (re-inlining the gate drops the rollback);
//  2. the gate runs BEFORE any package byte moves: before the
//     stagedOrLiveBytes download of the tollgate-wrt asset and before the
//     sshUploadPipe push;
//  3. the installed version comes from readInstalledPkgVersionE, and the
//     probe runs before the gate consumes it;
//  4. the gate itself restores the wireless snapshot BEFORE jobFail (the
//     shared-exit ordering), and its messages interpolate both versions.
func TestStep6DowngradeGuardWiring(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "deploy.go", deployGoSrc, 0)
	if err != nil {
		t.Fatalf("parsing deploy.go: %v", err)
	}
	runDecl := deployFuncDecl(t, f, "runDeployment")

	// 1 — the gate guards a return, exactly like refuseMissingRequestedRelease.
	gateGuardsReturn := false
	ast.Inspect(runDecl, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		call, ok := ifs.Cond.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "refuseOlderRequestedRelease" {
			return true
		}
		if len(ifs.Body.List) == 1 {
			if _, ok := ifs.Body.List[0].(*ast.ReturnStmt); ok {
				gateGuardsReturn = true
			}
		}
		return true
	})
	if !gateGuardsReturn {
		t.Errorf("runDeployment no longer reaches its downgrade guard through " +
			"`if refuseOlderRequestedRelease(...) { return }` — the guard path " +
			"must use the shared gate so it cannot skip the wireless rollback")
	}

	runStart := fset.Position(runDecl.Body.Pos()).Offset
	runEnd := fset.Position(runDecl.Body.End()).Offset
	runBody := deployGoSrc[runStart:runEnd]

	// 2 — the guard must be the FIRST thing step 6 does with the package:
	// before the laptop-side download and before the SSH push.
	guardAt := strings.Index(runBody, "refuseOlderRequestedRelease(")
	if guardAt < 0 {
		t.Fatalf("runDeployment does not call refuseOlderRequestedRelease — the pre-install downgrade guard is not wired into step 6")
	}
	for _, marker := range []struct {
		src  string
		what string
	}{
		{`stagedOrLiveBytes(job, "tollgate-wrt `, "the tollgate-wrt download"},
		{"sshUploadPipe(client, pkgData", "the SSH push of the package bytes"},
	} {
		at := strings.Index(runBody, marker.src)
		if at >= 0 && guardAt > at {
			t.Errorf("the downgrade guard runs AFTER %s — the whole point of the card is that the router is never moved backwards because the guard fired too late", marker.what)
		}
		if at < 0 {
			t.Errorf("marker for %s not found in runDeployment — the wiring assertion is stale", marker.what)
		}
	}

	// 3 — the probe feeds the gate, before it.
	probeAt := strings.Index(runBody, "readInstalledPkgVersionE(")
	if probeAt < 0 {
		t.Fatalf("runDeployment does not read the installed version through readInstalledPkgVersionE — the guard cannot distinguish \"not installed\" from \"unreadable\"")
	}
	if probeAt > guardAt {
		t.Errorf("the version probe runs AFTER the guard — the gate must consume the probe result (version + transport error)")
	}

	// 4 — the gate's own body: restore before fail, both versions named.
	gate := funcBodyText(t, fset, f, "refuseOlderRequestedRelease")
	restoreAt := strings.Index(gate, "restoreWirelessOnFailure(")
	refusalFailAt := strings.Index(gate, "jobFail(job")
	if restoreAt < 0 || refusalFailAt < 0 || restoreAt > refusalFailAt {
		t.Errorf("refuseOlderRequestedRelease must restore the wireless snapshot BEFORE jobFail on the refusal path:\n%s", gate)
	}
	if !strings.Contains(gate, "jobFailAfterRestore(") {
		t.Errorf("refuseOlderRequestedRelease does not route the unreadable-version failure through jobFailAfterRestore — the transport-death path must use the shared exit too:\n%s", gate)
	}
	for _, interpolation := range []string{"installed", "requestedTag"} {
		if !strings.Contains(gate, interpolation) {
			t.Errorf("refuseOlderRequestedRelease does not interpolate %q — the operator-facing failure must name BOTH versions:\n%s", interpolation, gate)
		}
	}
}
