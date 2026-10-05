#!/usr/bin/env bash
# One-shot manual router test for v0.6.0-alpha2-rc17-scanpre4.
# Usage: curl -fsSL <raw-url> | bash
#
# Downloads the SHA256-verified prerelease binary for this OS/arch, checks it
# against the published SHA256SUMS, and launches the wizard.
set -euo pipefail

V=v0.6.0-alpha2-rc17-scanpre4
REPO=felixfelix-bot/tollgate-installer
BASE="https://github.com/$REPO/releases/download/$V"

case "$(uname -s)" in
  Linux)  os=linux  ;;
  Darwin) os=darwin ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64)   arch=amd64 ;;
  aarch64|arm64)  arch=arm64 ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

sha_check() { # $1 = file with sums, verify from cwd
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c "$1" --ignore-missing
  else shasum -a 256 -c "$1"; fi
}

f="tollgate-installer-$os-$arch"
d="${TMPDIR:-/tmp}/tg-manual-$V"
mkdir -p "$d"; cd "$d"

echo "== downloading $V ($f) =="
curl -fsSLO "$BASE/$f"
curl -fsSLO "$BASE/SHA256SUMS"
echo "== verifying sha256 =="
sha_check SHA256SUMS
chmod +x "$f"

echo
echo "======================================================"
echo " TollGate wizard $V is starting."
echo " Open the URL it prints in your browser, pick your router,"
echo " and deploy."
echo
echo " If it stalls: leave it running and send me the terminal"
echo " output. A wedged router fails the job with a reason"
echo " instead of spinning forever (30s SSH deadline + 3min"
echo " no-progress watchdog)."
echo
echo " If it fails AFTER the service restart with 'lost the SSH"
echo " transport': that is the wizard's own restart reloading"
echo " router networking under it. It now re-acquires the"
echo " session for up to 120s before giving up — send me the"
echo " output either way."
echo "======================================================"
echo
exec "./$f" -port "${TG_PORT:-8099}"
