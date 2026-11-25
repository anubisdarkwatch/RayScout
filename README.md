# RayScout — Orbic RC400L RF Hub + Optional XIAO ESP32-S3 / Wio-SX1262

RayScout runs cleanly alongside **EFF Rayhunter** on your **Orbic RC400L** hotspot (`http://192.168.1.1:8090`) and turns it into a standalone cellular/Wi-Fi situational awareness & wardriving rig—with plug-and-play support for a **Seeed XIAO ESP32-S3** (BLE + 2.4 GHz promiscuous Wi-Fi + optional UART GPS) and **Wio-SX1262** (915 MHz LoRa off-grid alert relay & passive LoRa logging).

## Features

### Standalone Orbic RC400L (`orbic-hub/`)
- **Zero-Conflict Rayhunter Coexistence:** Never touches `/dev/diag`. Polls Rayhunter's local HTTP API (`http://127.0.0.1:8080/api/qmdl-manifest`) to surface cellular anomalies and forwards live GPS coordinates to Rayhunter's `/api/gps` endpoint.
- **Dual-Band Wi-Fi Scanner (`2.4 + 5 GHz`):** Parses `iw scan` (with `iwlist` fallback) including Vendor-Specific Information Elements (e.g., ASTM F3411 `FA:0B:BC` Wi-Fi Beacon Drone Remote ID).
- **Cellular Tower Poller & Baseline Differ:** Non-exclusive AT polling for `LTE`/`GSM`/`WCDMA`/`NR` cell metadata; alerts on 2G downgrades, sudden neighbor-cell drops, or unmapped strong towers (via optional `/data/rayscout/cell_baseline.csv`).
- **Co-Moving / Persistent Device Tracker:** Signature-free detection of Wi-Fi/BLE devices following you across ≥3 distinct cell towers or >800m of GPS travel.
- **Hot-Reloadable `rules.json`:** Edit OUI, SSID, Vendor-IE, BLE Manufacturer ID, and BLE Service UUID watchlists live in the Web UI.
- **Wardriving Ecosystem Exports & Auto-Upload:**
  - Spec-compliant **WiGLE CSV v1.6** (`WIFI` + `LTE`/`GSM` + `BLE` in one file) with direct **WiGLE.net API** upload.
  - Direct **Watch Dogs Go Wars (`wdgwars.pl`)** CSV upload/export.
  - **GeoJSON** export (for QGIS, geojson.io, Wardrift/DeFlock cross-referencing).
- **Multi-Source GPS:** Accepts **NMEA-0183 over TCP/UDP port `10110`** (ShareGPS / GPS2IP on Android/iOS), **Phone Browser GPS Bridge**, or **XIAO ESP32-S3 UART GPS**.

### Optional Companion Sensor (`xiao-sensor/`)
- **`xiao_esp32s3`** (Seeed XIAO ESP32-S3): Time-sliced **BLE 5.0 scanner** + **2.4 GHz Wi-Fi Promiscuous sniffer** (probe request OUIs + deauth/disassoc flood counter) + optional UART GPS (`RX=GPIO44, TX=GPIO43`).
- **`xiao_esp32s3_sx1262`** (Seeed Wio-SX1262 + XIAO ESP32-S3): Adds **915 MHz LoRa off-grid alert relay** (transmits high-priority Rayhunter/RayScout alerts even when cellular is down) and passive LoRa packet/RSSI logging using the B2B pinout (`SCK=7, MISO=8, MOSI=9, CS=41, BUSY=40, DIO1=39, RST=42`).

---

## Quick Start (Windows)

### 1. Prerequisites
- **Go 1.22+** (for building the Orbic ARMv7 binary): `winget install GoLang.Go`
- **ADB** (for pushing to your rooted Rayhunter Orbic): `winget install Google.PlatformTools`
- **PlatformIO** (optional, for flashing the XIAO ESP32-S3 boards): `pip install platformio`

### 2. One-Click Install on Your Orbic RC400L (No Linux Commands!)
1. Plug your powered-on Orbic (with Rayhunter installed) into your PC with a data-capable USB-C cable.
2. Double-click **`dist\RayScout-Installer.exe`**.
3. Choose **`[1] Install / Update RayScout on Orbic`** (or **`[2] Temporary Test Run`** if you want to try it until reboot without touching boot files).
4. The installer automatically pushes the ARMv7 binary, sets up USB port forwarding, and opens **`http://localhost:8090`** in your browser — **no Wi-Fi switching or Linux shell required!**
   - Over Orbic Wi-Fi (on your phone/laptop): `http://192.168.1.1:8090`
   - EFF Rayhunter remains untouched at: `http://192.168.1.1:8080` (or `http://localhost:8080` over USB)

### 3. Preview Locally in Demo Mode (No Orbic Needed)
Double-click **`Launch-Demo.bat`** (or run `.\dist\rayscout.exe -demo -rules .\rules.json`) to explore simulated Wi-Fi, LTE handoffs, a Co-Moving alert, a Remote ID drone hit, BLE sightings, and WiGLE/GeoJSON downloads at **http://localhost:8090**.

### 4. Rebuilding From Source (Optional)
```powershell
.\scripts\build-orbic.ps1 -IncludeWindows
```

### 4. Configure GPS (Standalone Mode)
For WiGLE/WDGWars wardriving without an external GPS module:
- **Best mobile option (works over plain HTTP/LAN):** Install **ShareGPS** (Android) or **GPS2IP** (iOS), connect your phone to the Orbic Wi-Fi, and stream **NMEA over TCP or UDP to `192.168.1.1:10110`**.
- RayScout automatically locks onto the NMEA stream and mirrors fixes to Rayhunter's `/api/gps`.

### 5. Flash Your XIAO ESP32-S3 / Wio-SX1262 Companion
1. Copy `xiao-sensor/include/secrets.example.h` to `xiao-sensor/include/secrets.h` (gitignored) and set `ORBIC_WIFI_SSID` and `ORBIC_WIFI_PASS` to your Orbic's Wi-Fi credentials.
2. Flash your chosen board:
```powershell
cd xiao-sensor

# Bare Seeed XIAO ESP32-S3:
pio run -e xiao_esp32s3 -t upload

# Seeed Wio-SX1262 + XIAO ESP32-S3 kit:
pio run -e xiao_esp32s3_sx1262 -t upload
```
Power the XIAO from a USB power bank (or car USB port) near the Orbic—it will automatically join the Orbic AP and light up **BLE + Wi-Fi Probes + Deauths (+ SX1262 LoRa)** inside the RayScout dashboard!
