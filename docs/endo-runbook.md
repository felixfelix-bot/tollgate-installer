# TollGate — GL-MT6000 Setup Guide

## What you need

- A GL-MT6000 router (power cable, ethernet cable)
- A laptop with WiFi or ethernet
- A Lightning wallet (e.g. Wallet of Satoshi, Muun, Zeus) or a Cashu wallet

## Step 1: Download the wizard

Download the wizard (`tollgate-installer`) for your laptop from the
[OpenTollGate/tollgate-installer releases](https://github.com/OpenTollGate/tollgate-installer/releases/latest):

- **macOS (Intel)**: `tollgate-installer-darwin-amd64`
- **macOS (Apple Silicon M1/M2/M3)**: `tollgate-installer-darwin-arm64`
- **Windows**: `tollgate-installer-windows-amd64.exe`
- **Linux**: `tollgate-installer-linux-amd64`

## Step 2: Connect the router

1. Plug the GL-MT6000 into power.
2. Connect one end of the ethernet cable to your laptop.
3. Connect the other end to the router's **LAN port** (not WAN).
4. Wait 60 seconds for the router to boot.

The router will be at **192.168.1.1** by default.

## Step 3: Run the wizard

**macOS:** Open Terminal, navigate to the download, and run:
```bash
chmod +x tollgate-installer-darwin-*
./tollgate-installer-darwin-amd64    # Intel Mac
./tollgate-installer-darwin-arm64    # Apple Silicon Mac
```

**Windows:** Double-click `tollgate-installer-windows-amd64.exe`

**Linux:**
```bash
chmod +x tollgate-installer-linux-amd64
./tollgate-installer-linux-amd64
```

Your browser will open automatically at `http://localhost:8099`.

If it doesn't, open your browser and go to: **http://localhost:8099**

## Step 4: Select your router

The wizard scans your network automatically. You should see:

> **192.168.1.1 — GL-MT6000 — OpenWrt 25.12.0**

Click on it to select it.

If no router appears:
- Make sure you're connected via ethernet to the router's LAN port
- Try clicking "Scan Again"
- Check that the router has been powered on for at least 60 seconds

## Step 5: Set the admin password

Enter a password for the router's admin account. You'll need this to SSH into the router later if needed.

- Enter the password twice (they must match)
- **Write this down** — there is no password recovery without it

## Step 6: Configure upstream internet

Choose how the router connects to the internet:

**Option A — Ethernet WAN (recommended):**
Connect an ethernet cable from your wall/modem to the router's **WAN port**.
No configuration needed — just plug it in.

**Option B — WiFi uplink:**
If you want the router to connect to an existing WiFi network:
1. Select "WiFi Client"
2. Enter the SSID (network name) of your upstream WiFi
3. Enter the WiFi password

## Step 7: Enter your Lightning address

Enter the Lightning address where you want to receive payments. This is where the sats from people buying internet access will go.

Example: `you@walletofsatoshi.com`

The router supports multiple mints:
- coinos.io
- minibits.cash
- testnut.cashu.exchange

Price is set to **1 sat per 21 MB** by default.

## Step 8: Deploy

Click **"Deploy TollGate"**. The wizard will:
1. SSH into the router
2. Set the admin password
3. Configure upstream connectivity
4. Set your Lightning address
5. Restart all services

This takes about 30 seconds. You'll see a live progress log.

## Step 9: Success

When deployment completes, you'll see:
- The router's new IP address
- The WiFi SSID clients should connect to
- The portal URL

**Connect to the router's WiFi** and try opening a website. You should see the TollGate captive portal asking for payment.

## How people pay for internet

When someone connects to your router's WiFi:
1. Their browser shows the **TollGate portal**
2. They choose a payment method: **Lightning** or **Cashu**
3. They pay 1 sat per 21 MB of data
4. Internet access is granted instantly

The sats go to your Lightning address.

## Accessing the portal

Once deployed, the captive portal is served by the router:
- **http://tollgate.lan** (TollGate portal — if DNS is configured)
- **http://<router-ip>:2051/splash.html** (direct portal access)

LuCI (OpenWrt admin) remains available at **http://<router-ip>:8080**
using the password you set in Step 5.

## Troubleshooting

**Router not found by wizard:**
- Check ethernet cable is in LAN port, not WAN
- Try `ping 192.168.1.1` in a terminal
- Factory reset the router (hold reset button 10 seconds)

**Captive portal not showing:**
- Make sure you're connected to the router's WiFi, not your home WiFi
- Try opening `http://neverssl.com` in a browser (forces redirect)
- Clear browser cache

**Payments not working:**
- Verify your Lightning address is correct
- Check that the router has internet (WAN or WiFi uplink)
- Check tollgate status: SSH in and run `wget -qO- http://localhost:2121/`

## Technical details

- Router: GL-MT6000 ( MediaTek MT7986A, 1GB RAM, aarch64)
- Firmware: OpenWrt 25.12.0
- Backend: OpenTollGate tollgate-module-basic-go v0.5.0
- Portal: `tollgate-captive-portal-site` (ships inside the tollgate-wrt package)
- Captive portal: nodogsplash 5.0.2
- Pricing: NIP-61 kind 10021 (1 sat per 21MB, 3 mints)

### Supported routers

| Device | Status | Sysupgrade image |
|---|---|---|
| GL-MT6000 | Fully tested | [OpenWrt filogic](https://downloads.openwrt.org/releases/25.12.5/targets/mediatek/filogic/) — `glinet_gl-mt6000-squashfs-sysupgrade.bin` |
| GL-MT3000 | Tested (v0.3.8-alpha) | [OpenWrt filogic](https://downloads.openwrt.org/releases/25.12.5/targets/mediatek/filogic/) — `glinet_gl-mt3000-squashfs-sysupgrade.bin` |

Firmware download page for all MediaTek filogic devices:
https://downloads.openwrt.org/releases/25.12.5/targets/mediatek/filogic/

### Deployment sources (what the wizard downloads)

The wizard pins its downloads in `deploy.go` (`pins_test.go` enforces that
every pin returns HTTP 200):

| Artifact | Source |
|---|---|
| `tollgate-wrt_v0.5.0_aarch64_cortex-a53.ipk` | [OpenTollGate/tollgate-module-basic-go v0.5.0](https://github.com/OpenTollGate/tollgate-module-basic-go/releases/tag/v0.5.0) |

The captive portal ships **inside** the tollgate-wrt package under
`/etc/tollgate/tollgate-captive-portal-site` — the wizard does not download
it separately. The fw4/nftables enforcement rules (upstream PR #283) also
ship inside the ipk under `/etc/nftables.d/`.

There is **no admin panel** in v1. OpenTollGate ships no standalone admin
dashboard yet, so the wizard does not deploy or download one.

---

*This runbook was validated on a live GL-MT6000 running OpenWrt 25.12.0.*
