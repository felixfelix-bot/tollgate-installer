# tollgate-installer — TollGate router setup wizard

Cross-platform onboarding wizard that turns an OpenWrt router into a TollGate
Bitcoin WiFi access point. Single Go binary — serves a web UI that auto-discovers
routers on your LAN and deploys the tollgate-wrt backend over SSH.

```
┌─ Your Laptop ────────────────────────┐
│                                     │
│  tollgate-installer binary                │
│  └─ web UI at http://localhost:8099 │
│     └─ scans LAN for routers        │
│     └─ deploys via SSH              │
│                                     │
└─────────┬───────────────────────────┘
          │ SSH
          ▼
┌─ Router (OpenWrt) ──────────────────┐
│  tollgate-wrt      (:2121) backend  │
│  nodogsplash       (:2050) portal   │
│  uhttpd :2051      captive portal   │
│  LuCI :8080        OpenWrt admin    │
└─────────────────────────────────────┘
```

## Quick start

1. **Flash your router to OpenWrt** (the wizard does NOT flash firmware). For
   GL.iNet routers, use the web UI at `http://192.168.8.1` → Advanced →
   Upload Firmware → untick "Keep settings".

   Alternatively, SSH in and sysupgrade:
   ```sh
   # GL-MT3000 on OpenWrt 25.12.5 (clean install, no config kept)
   curl -O https://downloads.openwrt.org/releases/25.12.5/targets/mediatek/filogic/openwrt-25.12.5-mediatek-filogic-glinet_gl-mt3000-squashfs-sysupgrade.bin
   cat openwrt-25.12.5-mediatek-filogic-glinet_gl-mt3000-squashfs-sysupgrade.bin | \
     ssh root@192.168.1.1 'cat > /tmp/sysupgrade.bin && sysupgrade -n /tmp/sysupgrade.bin'
   ```
   See [OpenWrt firmware downloads](https://downloads.openwrt.org/releases/25.12.5/targets/mediatek/filogic/)
   for other devices.

2. **Set a root password:**
   ```sh
   ssh root@192.168.1.1
   passwd
   ```

3. **Download the wizard** from the
   [latest release](https://github.com/OpenTollGate/tollgate-installer/releases/latest):

   | OS | File |
   |---|---|
   | macOS (Intel) | `tollgate-installer-darwin-amd64` |
   | macOS (Apple Silicon) | `tollgate-installer-darwin-arm64` |
   | Linux (x86_64) | `tollgate-installer-linux-amd64` |
   | Windows | `tollgate-installer-windows-amd64.exe` |

4. **Run it:**
   ```sh
   chmod +x tollgate-installer-*
   ./tollgate-installer-darwin-arm64   # replace with your OS
   ```

5. **Browser opens at** `http://localhost:8099` — follow the wizard:
   - It scans your network and lists detected routers
   - Select your router, enter the root password
   - Choose upstream connection (Ethernet WAN or WiFi repeater)
   - Enter your Lightning address (where payouts go)
   - Click **"Deploy TollGate"**

6. After ~30 seconds: connect to the `TollGate-XXXX` WiFi (4 random chars,
   all-caps) and open any website — the captive portal appears with payment
   options.

## What the wizard does

The deployment runs a sequence of steps over SSH:

| # | Step | Action |
|---|------|--------|
| 1 | Verify SSH | Connects, reads `/etc/openwrt_release` |
| 2 | Check firmware | Parses OpenWrt version |
| 3 | Set password | Sets the root password you entered |
| 4 | Configure upstream | WiFi STA mode or WAN passthrough |
| 5 | Install tollgate-wrt | Downloads the .ipk/.apk on the laptop, pushes over SSH, installs via opkg/apk |
| 6 | Brand router | Sets hostname + SSID to `TollGate-XXXX`, DNS to `tollgate.lan`, nodogsplash gateway name |
| 7 | Configure Lightning | Sets your Lightning address, dev split, margin, mint |
| 8 | Restart services | Restarts `tollgate-wrt` + `nodogsplash` + `rpcd` |
| 9 | Health check | Verifies the TollGate API is responding on `:2121` |

The tollgate-wrt `.ipk`/`.apk` ships the captive portal
(`/etc/tollgate/tollgate-captive-portal-site`), the rpcd plugin, and the
nftables enforcement rules — the wizard only installs the package and points
nodogsplash/uhttpd at it.

## Build from source

```sh
git clone https://github.com/OpenTollGate/tollgate-installer.git
cd tollgate-installer
go build -o tollgate-installer .
./tollgate-installer
```

Cross-compile for all platforms:

```sh
GOOS=darwin  GOARCH=arm64 go build -o dist/tollgate-installer-darwin-arm64 .
GOOS=darwin  GOARCH=amd64 go build -o dist/tollgate-installer-darwin-amd64 .
GOOS=linux   GOARCH=amd64 go build -o dist/tollgate-installer-linux-amd64 .
GOOS=windows GOARCH=amd64 go build -o dist/tollgate-installer-windows-amd64.exe .
```

## Prerequisites

- **Router** running OpenWrt (24.10.x or 25.x). The wizard does NOT flash
  firmware — see GL.iNet or OpenWrt docs for flashing.
- **SSH access** — port 22 open, root password set (empty on a fresh reset).
- **Upstream internet** — either Ethernet cable into the WAN port, or WiFi
  credentials for the router to join an existing network.
- **Lightning address** — where Bitcoin payments from customers route
  (e.g. `you@walletofsatoshi.com` or a raw LNURL).

## Architecture

This is a **thin UI wrapper**. All business logic lives in
[tollgate-module-basic-go](https://github.com/OpenTollGate/tollgate-module-basic-go).
The wizard only discovers routers, renders a deployment UI, and runs SSH
commands — no payment processing, identity derivation, or Nostr logic.

| Repo | Role |
|------|------|
| **tollgate-installer** (this repo) | Laptop-side onboarding wizard |
| [tollgate-captive-portal-site](https://github.com/OpenTollGate/tollgate-captive-portal-site) | Router-side captive portal (ships in the .ipk) |
| [tollgate-module-basic-go](https://github.com/OpenTollGate/tollgate-module-basic-go) | Payment backend (Cashu + Lightning) |

## License

MIT
