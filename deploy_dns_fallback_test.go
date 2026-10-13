package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// INSTALLER — the resolver fallback must actually recover, and must be revertible.
//
// Observed on the operator's hardware (scanpre5, 2026-10-05/06, MT3000-class
// router): the upstream handed out resolvers it does not serve, so
//
//	ping(1.1.1.1)=true dns(github.com)=false default-route=true public-dns=true
//	resolv.auto: nameserver 1.1.1.1 nameserver 1.0.0.1 nameserver 192.168.2.1
//
// i.e. a PUBLIC resolver answers (`public-dns=true`) while the router's own
// lookups fail. That is exactly the case upstreamVerdict calls "fixable on the
// router: point dnsmasq at 1.1.1.1, or let the installer apply that fallback" —
// and the deploy died at step 5 anyway, rolling the wireless back.
//
// Root cause: the fallback set ONLY `dhcp.@dnsmasq[0].server=<pub>`. On OpenWrt
// dnsmasq keeps reading /tmp/resolv.conf.d/resolv.conf.auto (the dead upstream
// resolvers) unless `noresolv` is set, so one extra `server=` never overrode
// them: `nslookup github.com` kept failing, dnsOK stayed false, and the deploy
// failed even though `nslookup github.com 1.1.1.1` succeeded.
//
// The second half is the rollback: the fallback rewrites the router's DHCP
// config, and the terminal-failure exit only restored the WIRELESS snapshot. A
// failed deploy must not leave a rewritten resolver config behind either.
//
// Tests, in the order the defect demands:
//
//  1. STATIC (below): the fallback's command sequence contains noresolv, and the
//     revert is wired into the same single failure exit the wireless restore uses
//     — a go/ast guard over the real deploy.go, in the same spirit as
//     TestDeployFailureSitesRestoreWireless, because no in-process test can drive
//     upstreamOnline without a router.
//  2. PURE: the decision + the exact uci command list live in planDNSFallback /
//     dnsFallbackApplyCmd / rollbackDNSCmd, which are asserted directly.
//  3. BEHAVIOURAL: the failure exit actually invokes the resolver restore through
//     the dnsRollback seam.
//  4. REAL HARDWARE: the emitted command text was run on the bench router
//     (root@192.168.11.1) — see docs/resolver-fallback.md.

// TestDNSFallbackApplyCmdIsTheExactRouterCommand pins the apply command text: the
// snapshot must run BEFORE the mutation, the mutation must contain noresolv (the
// whole defect), and the snapshot must be FIRST-WRITER-WINS — upstreamOnline runs
// twice on configureSTA's reload-retry path, and an unconditional re-snapshot on
// the second application would record the first application's own rewrite
// (server=1.1.1.1, noresolv=1) as the "pre-deploy" baseline, so the revert would
// then faithfully "restore" the deploy's own mutation.
func TestDNSFallbackApplyCmdIsTheExactRouterCommand(t *testing.T) {
	for _, pub := range dnsFallbackResolvers {
		cmd := dnsFallbackApplyCmd(pub)

		// The whole defect: without noresolv, dnsmasq keeps reading the dead
		// upstream resolvers out of resolv.conf.auto and the extra server= never
		// wins — this is what failed the scanpre5 deploy at step 5.
		if !strings.Contains(cmd, "uci -q set dhcp.@dnsmasq[0].noresolv='1'") {
			t.Errorf("apply command for %s does not set noresolv — dnsmasq keeps consulting the dead resolv.conf.auto:\n%s", pub, cmd)
		}
		// noresolv must be part of the SAME uci commit as the server add, and
		// before it.
		setAt := strings.Index(cmd, "uci -q set dhcp.@dnsmasq[0].noresolv='1'")
		addAt := strings.Index(cmd, "uci -q add_list dhcp.@dnsmasq[0].server='"+pub+"'")
		commitAt := strings.Index(cmd, "uci commit dhcp")
		if addAt < 0 || setAt < 0 || commitAt < 0 || !(addAt < setAt && setAt < commitAt) {
			t.Errorf("apply command must add server, set noresolv, then commit (addAt=%d setAt=%d commitAt=%d):\n%s", addAt, setAt, commitAt, cmd)
		}
		// The command must snapshot the two options it rewrites, and BEFORE the
		// mutation (so the revert has a true pre-deploy baseline).
		for _, opt := range []string{"dhcp.@dnsmasq[0].server", "dhcp.@dnsmasq[0].noresolv"} {
			snapAt := strings.Index(cmd, "uci -q get "+opt+" >")
			mutAt := strings.Index(cmd, "uci -q add_list dhcp.@dnsmasq[0].server=")
			if opt == "dhcp.@dnsmasq[0].noresolv" {
				mutAt = setAt
			}
			if snapAt < 0 || snapAt > mutAt {
				t.Errorf("apply command must snapshot %s BEFORE rewriting it (snapAt=%d mutAt=%d):\n%s", opt, snapAt, mutAt, cmd)
			}
		}
		// First-writer-wins: a stale snapshot must not be overwritten. The
		// reload-retry path runs upstreamOnline twice; the second application
		// must not re-baseline against the first one's mutation.
		if !strings.Contains(cmd, "uci -q get "+pub) && !strings.Contains(cmd, "[ -f ") {
			t.Errorf("apply command is not snapshot-guarded — an unconditional re-snapshot would record the deploy's own first application as the pre-deploy baseline:\n%s", cmd)
		}
	}
}

// TestDNSFallbackPlanDecidesWhenTheFallbackApplies pins the decision: the
// fallback may rewrite the router's resolver config ONLY when the upstream
// answers ping, the router's own lookups fail, and a public resolver answers.
func TestDNSFallbackPlanDecidesWhenTheFallbackApplies(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		pingOK, dnsOK, publicDNSOK bool
		resolver                   string
		want                       bool
	}{
		{"the scanpre5 case: upstream up, router DNS dead, public answers", true, false, true, "1.1.1.1", true},
		{"dns already works", true, true, true, "1.1.1.1", false},
		{"upstream dead", false, false, true, "1.1.1.1", false},
		{"public DNS blocked too (nothing to fix on the router)", true, false, false, "1.1.1.1", false},
		{"no resolver supplied", true, false, true, "", false},
		{"no resolver supplied (whitespace)", true, false, true, "  ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, ok := planDNSFallback(tc.pingOK, tc.dnsOK, tc.publicDNSOK, tc.resolver)
			if ok != tc.want {
				t.Fatalf("planDNSFallback(...) = %v, want %v", ok, tc.want)
			}
			if !ok {
				return
			}
			if plan.resolver != "1.1.1.1" {
				t.Errorf("plan.resolver = %q, want the supplied resolver", plan.resolver)
			}
			if plan.apply != dnsFallbackApplyCmd(tc.resolver) {
				t.Errorf("plan.apply is not the pure apply command for %q:\n%s", tc.resolver, plan.apply)
			}
			if plan.revert != rollbackDNSCmd {
				t.Errorf("plan.revert is not the shared revert command:\n%s", plan.revert)
			}
		})
	}
}

// TestRollbackDNSCmdRestoresWhatTheApplyRewrote pins the revert command's
// contract: it restores EXACTLY the two options the apply rewrote, putting each
// back to its snapshotted state (ABSENT => deleted; EXISTED => its recorded
// value), restarts dnsmasq, and drops the snapshot scratch.
func TestRollbackDNSCmdRestoresWhatTheApplyRewrote(t *testing.T) {
	for _, want := range []string{
		`uci -q delete dhcp.@dnsmasq[0].server`,        // ABSENT => option deleted
		`uci -q add_list dhcp.@dnsmasq[0].server=`,     // EXISTED => each item put back
		`uci -q delete dhcp.@dnsmasq[0].noresolv`,      // ABSENT => option deleted
		`uci -q set dhcp.@dnsmasq[0].noresolv=`,        // EXISTED => value put back
		dnsmasqServerStateFile, dnsmasqServerValueFile, // it consults + drops the snapshot
		dnsmasqNoresolvStateFile, dnsmasqNoresolvValueFile,
		`rm -f`,                       // ...so a stale snapshot cannot leak
		`uci commit dhcp`,             // and commits the restore
		`/etc/init.d/dnsmasq restart`, // before reloading the forwarder
	} {
		if !strings.Contains(rollbackDNSCmd, want) {
			t.Errorf("rollbackDNSCmd does not %q — the revert would not undo the apply:\n%s", want, rollbackDNSCmd)
		}
	}
	// Snapshot-guarded: when this run never applied the fallback there is no
	// snapshot on the router, so the revert must touch nothing — same contract
	// as rollbackWirelessCmd, which is what lets ONE exit serve both.
	if !strings.HasPrefix(rollbackDNSCmd, "if [ -f ") {
		t.Errorf("rollbackDNSCmd is not snapshot-guarded — it would rewrite the resolver config of a deploy that never applied the fallback:\n%s", rollbackDNSCmd)
	}
}

// TestDNSFallbackCleanupCmdIsSnapshotScratchRemoval pins the success-path
// command: rm -f of exactly the four snapshot files and nothing else — no uci
// mutation at all.
func TestDNSFallbackCleanupCmdIsSnapshotScratchRemoval(t *testing.T) {
	for _, f := range []string{dnsmasqServerStateFile, dnsmasqServerValueFile, dnsmasqNoresolvStateFile, dnsmasqNoresolvValueFile} {
		if !strings.Contains(dnsFallbackCleanupCmd, f) {
			t.Errorf("dnsFallbackCleanupCmd does not remove %s:\n%s", f, dnsFallbackCleanupCmd)
		}
	}
	if strings.Contains(dnsFallbackCleanupCmd, "uci ") {
		t.Errorf("dnsFallbackCleanupCmd must not touch the resolver config (the fallback is the working config on success):\n%s", dnsFallbackCleanupCmd)
	}
}

// rollbackCall records one invocation of the dnsRollback seam (the same shape
// the wireless seam's rollbackCall uses, for the same reasons).
type dnsRollbackCall struct {
	client    *ssh.Client
	jobStatus string
}

// stubDNSRollback replaces the resolver-revert side effect with a recorder for
// one test, and ALSO neutralizes wirelessRollback — the same exit runs both, and
// the DNS tests here must observe the DNS seam without the wireless one
// panicking on a zero-value client.
func stubDNSRollback(t *testing.T, job *Job) *[]dnsRollbackCall {
	t.Helper()
	calls := &[]dnsRollbackCall{}
	orig, origWireless := dnsRollback, wirelessRollback
	dnsRollback = func(c *ssh.Client) {
		*calls = append(*calls, dnsRollbackCall{client: c, jobStatus: job.Status})
	}
	wirelessRollback = func(c *ssh.Client) {}
	t.Cleanup(func() {
		dnsRollback = orig
		wirelessRollback = origWireless
	})
	return calls
}

// TestTerminalFailureRevertsTheResolverFallback drives the shared
// terminal-failure exit with the fallback applied (STA committed, resolver
// rewrite in place) and asserts the resolver revert runs: BEFORE the job is
// marked failed, handed the live client, in both STA and WAN mode (the fallback
// itself can run in WAN mode, so its revert must not depend on the staCommitted
// gate).
func TestTerminalFailureRevertsTheResolverFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		staCommitted bool
	}{
		{"STA committed by this run (step 5 ran)", true},
		{"no STA committed (WAN mode — the fallback still owes its revert)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := newJob("192.168.1.1")
			client := new(ssh.Client)
			calls := stubDNSRollback(t, job)

			jobFailAfterRestore(job, client, tc.staCommitted, 6, "tollgate-wrt version mismatch", "installed 0.5.0, wanted 0.6.0")

			if len(*calls) != 1 {
				t.Fatalf("terminal failure made %d resolver-revert call(s), want exactly 1 — a failed deploy must not leave the router with a rewritten resolver config", len(*calls))
			}
			if got := (*calls)[0].client; got != client {
				t.Errorf("resolver revert was handed %p, want the live deploy client %p", got, client)
			}
			if got := (*calls)[0].jobStatus; got != "running" {
				t.Errorf("resolver revert ran while the job was already %q; it must run BEFORE the job is marked failed", got)
			}
			if job.Status != "failed" {
				t.Errorf("job.Status = %q, want \"failed\"", job.Status)
			}
		})
	}
}

// TestDNSFallbackResolversAreTheOnesTheVerdictNames pins the resolver list: it
// must stay the same two the verdict text names and the diagnostics print, or
// the operator-facing message would promise a resolver the fallback never tries.
func TestDNSFallbackResolversAreTheOnesTheVerdictNames(t *testing.T) {
	want := []string{"1.1.1.1", "9.9.9.9"}
	if len(dnsFallbackResolvers) != len(want) {
		t.Fatalf("dnsFallbackResolvers = %v, want %v", dnsFallbackResolvers, want)
	}
	for i := range want {
		if dnsFallbackResolvers[i] != want[i] {
			t.Errorf("dnsFallbackResolvers[%d] = %q, want %q", i, dnsFallbackResolvers[i], want[i])
		}
	}
}

// TestResolverFallbackSetsNoresolvAndIsRevertable is the static half: it parses
// the real deploy.go and pins the two things the field failure proved missing.
func TestResolverFallbackSetsNoresolvAndIsRevertable(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "deploy.go", deployGoSrc, 0)
	if err != nil {
		t.Fatalf("parsing deploy.go: %v", err)
	}

	probe := funcBodyText(t, fset, f, "upstreamOnline")
	if !strings.Contains(probe, "planDNSFallback(") {
		t.Errorf("upstreamOnline no longer decides the resolver fallback through planDNSFallback — " +
			"the decision and the command list must stay pure/testable (this is how the missing " +
			"noresolv survived: the command was an inline literal nothing asserted on)")
	}
	if !strings.Contains(probe, ".apply") {
		t.Errorf("upstreamOnline does not run the plan's apply command — the router command must come " +
			"from the pure command builder so it cannot drift from what the tests pin")
	}

	restore := funcBodyText(t, fset, f, "restoreWirelessOnFailure")
	if !strings.Contains(restore, "dnsRollback(") {
		t.Errorf("restoreWirelessOnFailure (the single terminal-failure exit) does not revert the " +
			"resolver fallback: a deploy that fails after the fallback ran leaves the router with a " +
			"rewritten resolver config")
	}

	if !strings.Contains(deployGoSrc, "var dnsRollback = func(client *ssh.Client) { rollbackDNS(client) }") {
		t.Error("the dnsRollback seam no longer delegates to rollbackDNS — the unit tests would then pin " +
			"behaviour the deploy does not have")
	}

	// The success path must drop the snapshot scratch the fallback wrote: at
	// that point the rewrite IS the working resolver config and must stay
	// applied, so a LATER failed run cannot "restore" a baseline the operator
	// already replaced.
	done := funcBodyText(t, fset, f, "runDeployment")
	if !strings.Contains(done, "dnsFallbackCleanupCmd") {
		t.Error("runDeployment's success path does not drop the fallback's snapshot scratch " +
			"(dnsFallbackCleanupCmd) — a later failed run would restore a dead pre-deploy baseline " +
			"and delete the resolver the operator is now using")
	}
}
