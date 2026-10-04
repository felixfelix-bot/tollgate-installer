#!/usr/bin/env bash
# tag-feed-pre22.sh — cut the v0.6.0-alpha4-pre22 release tag on
# FreedomTechFeed/packages, after proving the merged master really is the pre22
# pin. Read-only until the last step; refuses on any mismatch.
#
#   bash tag-feed-pre22.sh                # wait for the repin's CI, then tag
#   bash tag-feed-pre22.sh --dry-run      # every check, create nothing
#   bash tag-feed-pre22.sh --no-wait      # refuse instead of waiting for CI
#   bash tag-feed-pre22.sh --wait-min 90  # cap the wait (default 60)
#
# Needs: gh (authenticated as a user who can push the repo) and python3.
# Deliberately NOT `set -e`: every check reports its own reason.
set -uo pipefail

REPO="FreedomTechFeed/packages"
PR="48"
TAG="v0.6.0-alpha4-pre22"
EXPECT_MERGE="a1e1828c1e28dba7c49df84caec6b538a3458c53"
EXPECT_PKG_VERSION="0.6.0_alpha4_pre22"
EXPECT_SOURCE_VERSION="8b9ba86eed449c89f8da5376d9f4470ec0715515"
EXPECT_HASH="b439a433143f6261653d6de50e71658e7343366cf4306acdd3a221bb15d88271"
EXPECT_TAG="v0.6.0-alpha4"
OFFLINE_REPO="OpenTollGate/physical-router-test-automation"
OFFLINE_REF="4cf9074f21ae8ab2d00b8b918ea8d854d40de60f"

DRY=0; WAIT=1; WAIT_MIN=60; POLL_S=60
while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run)  DRY=1 ;;
        --no-wait)  WAIT=0 ;;
        --wait-min) WAIT_MIN="${2:-60}"; shift ;;
        --poll-s)   POLL_S="${2:-60}"; shift ;;
        *) printf 'unknown option: %s\n' "$1"; exit 2 ;;
    esac
    shift
done

FAIL=0
ok()   { printf '  OK    %s\n' "$1"; }
bad()  { printf '  FAIL  %s\n' "$1"; FAIL=1; }
warn() { printf '  WARN  %s\n' "$1"; }
info() { printf '  ..    %s\n' "$1"; }
apiget() { gh api "$@" 2>/dev/null; }

printf '== pre22 release tag\n'
printf '   repo %s\n   tag  %s\n   mode %s\n\n' "$REPO" "$TAG" \
    "$([ "$DRY" = 1 ] && printf 'dry-run' || printf 'live')"

printf -- '-- preconditions\n'
command -v gh >/dev/null 2>&1 || bad "gh not found on PATH"
command -v python3 >/dev/null 2>&1 || bad "python3 not found on PATH"
[ "$FAIL" = 0 ] || { printf '\nABORT: missing prerequisites.\n'; exit 1; }

WHO=$(apiget user -q .login)
if [ -z "${WHO:-}" ]; then
    bad "gh is not authenticated (run: gh auth login)"
    printf '\nABORT: cannot reach the API as an authenticated user.\n'
    exit 1
fi
ok "gh authenticated as $WHO"

PERM=$(apiget "repos/$REPO" -q .permissions.push)
if [ "${PERM:-}" = "true" ]; then
    ok "push permission on $REPO for $WHO"
elif [ "$DRY" = 1 ]; then
    warn "no push permission as $WHO — informational in --dry-run; the live run needs the maintainer account"
else
    bad "no push permission on $REPO as $WHO — the tag must be pushed by the maintainer account"
fi

printf -- '\n-- the merge being tagged\n'
MERGED=$(apiget "repos/$REPO/pulls/$PR" -q .merged)
if [ "${MERGED:-}" = "true" ]; then
    ok "PR #$PR (the pre22 repin) is merged"
else
    bad "PR #$PR is not merged (got '${MERGED:-?}') — do not tag"
fi

HEAD=$(apiget "repos/$REPO/git/ref/heads/master" -q .object.sha)
if [ "${HEAD:-}" = "$EXPECT_MERGE" ]; then
    ok "master == $EXPECT_MERGE (the #$PR merge commit)"
else
    bad "master is ${HEAD:-?}, expected $EXPECT_MERGE"
    info "master moved since this pin was verified — re-check the pin before tagging"
fi

printf -- '\n-- the pin actually on master (fetched from GitHub, not a local clone)\n'
MK=$(apiget -H "Accept: application/vnd.github.raw" "repos/$REPO/contents/net/tollgate-wrt/Makefile?ref=master")
if [ -z "$MK" ]; then
    bad "could not fetch net/tollgate-wrt/Makefile from master"
else
    for pair in "PKG_VERSION|$EXPECT_PKG_VERSION" "PKG_SOURCE_VERSION|$EXPECT_SOURCE_VERSION" "PKG_HASH|$EXPECT_HASH" "PKG_SOURCE_TAG|$EXPECT_TAG"; do
        name=${pair%%|*}; want=${pair#*|}
        got=$(printf '%s\n' "$MK" | sed -n "s/^${name}:=//p" | head -n1)
        if [ "$got" = "$want" ]; then
            ok "$name == $want"
        else
            bad "$name is '${got:-<missing>}', expected $want"
        fi
    done
fi

printf -- '\n-- the offline (WAN-less) bundle this release will build\n'
MISSING=""
for f in install-offline.sh install-router.sh templates/99z-mgmt-keepalive; do
    apiget "repos/$OFFLINE_REPO/contents/scripts/offline/$f?ref=$OFFLINE_REF" -q .size >/dev/null 2>&1 || MISSING="$MISSING $f"
done
if [ -z "$MISSING" ]; then
    ok "OFFLINE_INSTALLER_REF $OFFLINE_REF carries install-offline.sh, install-router.sh and the keepalive seed"
else
    bad "cannot resolve${MISSING} at ${OFFLINE_REPO}@$OFFLINE_REF — the release jobs would fail once the tag exists"
fi

if [ "$FAIL" != 0 ]; then
    printf '\nABORT: a precondition failed. No tag was created.\n'
    exit 1
fi

printf -- '\n-- CI on the repin commit\n'
PR_SHA=$(apiget "repos/$REPO/pulls/$PR" -q .head.sha)
PR_SHA=${PR_SHA:-$EXPECT_MERGE}
printf '   checking %s\n' "$PR_SHA"

ci_poll() {
    apiget "repos/$REPO/commits/$PR_SHA/check-runs?per_page=100" | python3 -c '
import json, sys
raw = sys.stdin.read().strip()
if not raw:
    print("NO_DATA"); sys.exit(0)
runs = json.loads(raw).get("check_runs", [])
if not runs:
    print("NO_DATA"); sys.exit(0)
done = [r for r in runs if (r.get("status") or "") == "completed"]
open_ = [r for r in runs if (r.get("status") or "") != "completed"]
red = [r for r in done if (r.get("conclusion") or "") not in ("success", "skipped", "neutral")]
print("%d/%d %d %s" % (len(done), len(runs), len(red),
      ",".join("%s=%s" % (r["name"], r.get("conclusion")) for r in red[:6])))
'
}

DEADLINE=$(( $(date +%s) + WAIT_MIN * 60 ))
STATUS=""; RED=""; NDONE=""; NTOT=""; REDLIST=""
while : ; do
    LINE=$(ci_poll)
    if [ "$LINE" = "NO_DATA" ]; then
        bad "no check-runs readable for $PR_SHA"
        break
    fi
    NDONE=$(printf '%s' "$LINE" | awk '{print $1}' | cut -d/ -f1)
    NTOT=$(printf '%s' "$LINE" | awk '{print $1}' | cut -d/ -f2)
    RED=$(printf '%s' "$LINE" | awk '{print $2}')
    REDLIST=$(printf '%s' "$LINE" | cut -d' ' -f3-)
    if [ "${RED:-0}" != "0" ]; then
        bad "$RED check(s) not green on the repin commit: $REDLIST"
        break
    fi
    if [ "${NDONE:-0}" = "${NTOT:-0}" ]; then
        ok "all $NTOT check(s) on the repin commit are green"
        break
    fi
    if [ "$WAIT" = 0 ]; then
        bad "$NDONE/$NTOT checks done — CI still running and --no-wait was given"
        break
    fi
    if [ "$(date +%s)" -ge "$DEADLINE" ]; then
        bad "$NDONE/$NTOT checks done after ${WAIT_MIN} min — giving up (re-run to continue waiting)"
        break
    fi
    printf '  ..    %s/%s checks done, waiting %ss (kill and re-run any time)\n' "$NDONE" "$NTOT" "$POLL_S"
    sleep "$POLL_S"
done

printf -- '\n-- the tag itself\n'
EXISTING=""
if REFJSON=$(apiget "repos/$REPO/git/ref/tags/$TAG"); then
    EXISTING=$(printf '%s' "$REFJSON" | python3 -c 'import json,sys
try:
    print(json.load(sys.stdin).get("object", {}).get("sha", ""))
except Exception:
    print("")')
fi
if [ -n "${EXISTING:-}" ]; then
    if [ "$EXISTING" = "$EXPECT_MERGE" ]; then
        printf '  ..    %s already exists at %s — nothing to do\n' "$TAG" "$EXISTING"
        printf '\nALREADY TAGGED. Watch the tag-triggered release:\n  https://github.com/%s/actions/workflows/release-publish.yml\n' "$REPO"
        exit 0
    fi
    bad "$TAG already exists at $EXISTING, not $EXPECT_MERGE — refusing to move a published tag"
else
    ok "$TAG does not exist yet"
fi

if [ "$FAIL" != 0 ]; then
    printf '\nABORT: at least one check failed. No tag was created.\n'
    exit 1
fi

if [ "$DRY" = 1 ]; then
    printf '\nDRY RUN: every check passed. Re-run without --dry-run to create the tag.\n'
    exit 0
fi

printf -- '\n-- creating the tag\n'
CREATED=$(apiget -X POST "repos/$REPO/git/refs" -f "ref=refs/tags/$TAG" -f "sha=$EXPECT_MERGE" -q .ref)
case "${CREATED:-}" in
    refs/tags/*)
        ok "created $CREATED at $EXPECT_MERGE"
        ;;
    *)
        bad "tag creation returned '${CREATED:-<nothing>}'"
        printf '\nABORT: the tag was NOT created.\n'
        exit 1
        ;;
esac

cat <<EOF

== tag pushed. The release workflow is now running (tag-triggered).

  watch:    https://github.com/$REPO/actions/workflows/release-publish.yml
  release:  https://github.com/$REPO/releases/tag/$TAG

The release stays a DRAFT until every per-arch package AND the signed
SHA256SUMS manifest are uploaded, then it publishes in one step — so a
half-built release never resolves for an installer. Expect, per arch, both
tollgate-wrt_${EXPECT_PKG_VERSION}_<arch>.apk and .ipk, plus the offline
(WAN-less) bundles, all covered by the signed manifest.

When it goes green, prove the fix is downloadable:

  curl -sIL https://github.com/$REPO/releases/download/$TAG/tollgate-wrt_${EXPECT_PKG_VERSION}_aarch64_cortex-a53.apk | head -1

The installer finds the tag by itself from the alpha channel
(TOLLGATE_FEED_CHANNEL=alpha, or pin TOLLGATE_FEED_RELEASE_TAG=$TAG).

To undo before anything installs it:

  gh release delete $TAG --yes --cleanup-tag

EOF
