# Resolver fallback and its revert (step 5, `upstreamOnline`)

## The failure this heals

Some upstreams hand the router DHCP resolvers they do not actually serve:

```
ping(1.1.1.1)=true dns(github.com)=false default-route=true public-dns=true
resolv.auto: nameserver 1.1.1.1 nameserver 1.0.0.1 nameserver 192.168.2.1 search Realtek
```

The first hop answers and a public resolver answers, but the router's own
lookups fail, because dnsmasq forwards to the dead entries in
`/tmp/resolv.conf.d/resolv.conf.auto`. Adding one `server=` line is not enough:
dnsmasq keeps the upstream list too and still tries the dead servers. Without
`noresolv`, `nslookup github.com` keeps failing, `dnsOK` stays false, and the
deploy dies at step 5 even though `nslookup github.com 1.1.1.1` succeeded —
the exact case the "public resolver answers" verdict promises to heal.

## The fix (what runs on the router)

`upstreamOnline` (deploy.go) applies the fallback through the pure planner
`planDNSFallback` / `dnsFallbackApplyCmd(resolver)`. For each public resolver in
`dnsFallbackResolvers` (exactly `1.1.1.1`, `9.9.9.9` — the two the verdict text
names), the emitted command:

1. snapshots the two dnsmasq options it is about to rewrite (see below),
2. `uci -q add_list dhcp.@dnsmasq[0].server='<resolver>'`
   (with a `del_list` first so a retry cannot duplicate the entry),
3. `uci -q set dhcp.@dnsmasq[0].noresolv='1'` — set BEFORE the commit, so
   dnsmasq ignores the dead `resolv.conf.auto` and uses the explicit
   `server=` entries only,
4. `uci commit dhcp && /etc/init.d/dnsmasq restart; sleep 3`.

`nslookup github.com` is then re-tested, as before.

## Snapshot-and-revert contract

The fallback rewrites the router's DHCP config, so it participates in the
deploy's rollback contract (see `deploy-failure-rollback.md` for the full
wireless story):

- **Snapshot first, mutate after.** The pre-change value AND existence of each
  rewritten option (`dhcp.@dnsmasq[0].server`, `dhcp.@dnsmasq[0].noresolv`) is
  recorded on the router in the same command that starts the rewrite: a value
  file plus a `state` file saying `EXISTED` or `ABSENT` — the same
  existence+value shape the STA script uses for `network.wwan`, because uci
  has no per-option export.
- **First-writer-wins.** The snapshot write is guarded (`[ -f <state> ]`), so
  the fallback's second application (upstreamOnline runs twice on
  configureSTA's reload-retry path) cannot overwrite the true baseline with
  the first application's own rewrite.
- **Revert on failure.** The deploy's single failure gate
  (`restoreWirelessOnFailure`) also invokes `dnsRollback`, which runs
  `rollbackDNSCmd`: snapshot-guarded (a deploy that never applied the fallback
  touches nothing), it restores each option exactly — deleted when it was
  ABSENT pre-deploy, else its pre-change value, with the `server=` list items
  restored individually (uci prints a list option as ONE space-separated line,
  so the restore iterates words, not lines) — then commits, restarts dnsmasq,
  and drops the snapshot scratch. A failed deploy must not leave a rewritten
  resolver config any more than it may leave committed radios.
- **Cleanup on success.** On success `dnsFallbackCleanupCmd` drops the snapshot
  scratch only: the rewrite is now the router's working resolver config and
  must stay applied, and the stale baseline must not survive to let a LATER
  failed run "restore" it over the operator's working resolvers.

## Where the behaviour is pinned

`deploy_dns_fallback_test.go`:

- static wiring guards (pure planner present; `upstreamOnline` uses it;
  failure gate calls `dnsRollback`), including the exact `dnsRollback` seam
  declaration,
- `TestDNSFallbackApplyCmdIsTheExactRouterCommand` — snapshot-before-mutation,
  `noresolv` set before commit, first-writer-wins snapshot,
- `TestDNSFallbackResolversAreTheOnesTheVerdictNames` — exactly 1.1.1.1/9.9.9.9,
- rollback-command tests — snapshot-guard, word-split list restore,
  `ABSENT` ⇒ delete, scratch dropped,
- a behavioural revert test through the `dnsRollback` seam.

## Security scope

DNS/forwarder change only. Host-key trust, transport verification and every
"skip verification" path are untouched — see the task card's out-of-scope note.