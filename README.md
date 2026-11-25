# RayScout

**Turn your EFF Rayhunter Orbic RC400L hotspot into a pocketable RF situational awareness, counter-surveillance, and multi-band wardriving rig — with zero Linux commands required to install.**

RayScout runs cleanly side-by-side with [**EFF Rayhunter**](https://github.com/EFForg/rayhunter) (and optionally [**DagShell**](https://github.com/dagnazty/DagShell)) on the Orbic RC400L. It works **100% standalone** out of the box for dual-band Wi-Fi, cellular monitoring, and wardriving, and automatically expands to **BLE 5.0, 2.4 GHz Wi-Fi probe/deauth sniffing, and 915 MHz LoRa** the moment you power on an optional **Seeed Studio XIAO ESP32-S3** or **Wio-SX1262** companion board.

---

## Hardware Capabilities

| Mode | Hardware | What It Monitors & Does |
| :--- | :--- | :--- |
| **Standalone Core** | **Orbic RC400L** (with EFF Rayhunter) | Dual-band **2.4 GHz & 5 GHz Wi-Fi AP scanning** (including Beacon Vendor-Specific IEs for FAA/ASTM F3411 Drone Remote ID), **LTE/GSM/WCDMA/NR cell-tower logging**, live **EFF Rayhunter sentinel banner** (warnings, QMDL size, battery %, 5-bar cellular signal), **Co-Moving / Persistent AP tracking**, embedded dark-mode Web UI, and **WiGLE v1.6 / WDGWars / Wardrift / GeoJSON** export & upload. |
| **+ BLE & Probe Companion** | **Seeed XIAO ESP32-S3** | Adds **BLE 5.0 scanning** (UUIDs, Manufacturer IDs, BLE Remote ID, trackers), **2.4 GHz Wi-Fi promiscuous probe-request sniffing**, **deauth/disassoc flood detection**, and optional **UART GPS** (`GPIO43/44`). |
| **+ Sub-GHz LoRa Relay** | **Seeed Wio-SX1262 + XIAO ESP32-S3** | Everything above **plus** **915 MHz LoRa off-grid alert relaying** (broadcasts high-priority Rayhunter/RayScout alerts even when cellular is down) and passive LoRa packet/RSSI logging. |

```text
┌────────────────────────────────────────────────────────────────────┐
│                    ORBIC RC400L (Standalone Hub)                   │
│  • EFF Rayhunter Bridge (:8080) — live warnings, battery & GPS     │
│  • Dual-Band Wi-Fi Scanner (2.4 & 5 GHz + Beacon Remote ID IEs)    │
│  • Non-Exclusive Modem AT Cell Poller (never touches /dev/diag)    │
│  • Co-Moving Device Tracker + Cell Anomaly Differ + rules.json     │
│  • WiGLE v1.6 CSV / GeoJSON + WiGLE, WDGWars & Wardrift Uploaders  │
│  • Offline Web Dashboard (:8090) + Phone NMEA GPS Listener (:10110)│
└─────────────────────────────────▲──────────────────────────────────┘
                                  │ Wi-Fi HTTP POST (/api/v1/ingest)
                                  │ (Returns pending LoRa alert queue)
┌─────────────────────────────────┴──────────────────────────────────┐
│         OPTIONAL: Seeed XIAO ESP32-S3 (+ Wio-SX1262 Kit)           │
│  • Time-sliced BLE 5.0 Scanner + 2.4 GHz Wi-Fi Probe/Deauth Sniffer│
│  • Optional Wio-SX1262 (915 MHz LoRa alert TX & passive RX)        │
│  • Optional UART GPS module on free XIAO pins (RX=44, TX=43)       │
└────────────────────────────────────────────────────────────────────┘
```

---

## Quick Start: Installing on Your Orbic RC400L (No Linux Skills Needed)

### Prerequisites
* An **Orbic RC400L** hotspot already running [**EFF Rayhunter**](https://github.com/EFForg/rayhunter) (with USB ADB / root access enabled).
* A Windows PC with **ADB** installed (`scoop install adb` or `winget install Google.PlatformTools`).
* A **data-capable USB-C cable** (some charging-only cables will not detect ADB).

### Step 1 — Get or Build `RayScout-Installer.exe`
* **Easiest:** Download `RayScout-Installer.exe` from the **Releases** page (if published), **or**
* **Build from source** (requires Go 1.22+ via `scoop install go` or `winget install GoLang.Go`):
  ```powershell
  git clone https://github.com/anubisdarkwatch/RayScout.git
  cd RayScout
  .\scripts\build-orbic.ps1 -IncludeWindows
  ```
  This cross-compiles the static ARMv7 Orbic binary and bundles everything (binary, `rules.json`, default config, and boot script) into a single **`dist\RayScout-Installer.exe`**.

### Step 2 — One-Click Flash Over USB
1. Power on your Orbic RC400L and plug it into your PC via USB-C.
2. Double-click **`dist\RayScout-Installer.exe`**.
3. Confirm it shows **`Orbic RC400L detected via USB`** and your root method (`direct`, `rootshell`, or `nc24`).
4. Choose:
   * **`[1] Install / Update RayScout on Orbic`** — installs to `/data/rayscout`, opens firewall ports `8090` & `10110`, sets up auto-start on boot, and leaves EFF Rayhunter completely untouched.
   * **`[2] Temporary Test Run`** — runs until your next reboot without modifying boot scripts.
5. The installer automatically forwards USB ports and opens **`http://localhost:8090`** in your browser!

> **Updating later:** Running option `[1]` again safely upgrades the binary while keeping your existing `/data/rayscout/config.json` (saved WiGLE, WDGWars, and Wardrift credentials).

---

## Accessing the Dashboard (PC vs. Phone)

| Device | Connection | RayScout URL | EFF Rayhunter URL |
| :--- | :--- | :--- | :--- |
| **Phone / Tablet** | Connected to Orbic Wi-Fi AP | `http://192.168.1.1:8090` | `http://192.168.1.1:8080` |
| **PC (over USB)** | USB-C cable (via Installer option `[3]`) | `http://localhost:8090` | `http://localhost:8080` |

> **Note:** On your phone, always use **`http://192.168.1.1:8090`** (not `localhost`, which points to the phone itself).

---

## Phone GPS Setup for Wardriving (WiGLE, WDGWars & Wardrift)

Because the Orbic RC400L has no built-in GPS receiver, RayScout listens for standard **NMEA-0183** GPS streams on port **`10110`** (UDP & TCP) — and automatically forwards valid GPS fixes to **EFF Rayhunter's `/api/gps`** as well.

1. Install a lightweight NMEA GPS streamer app on your phone:
   * **Android (Google Play):** *Share GPS: NMEA, gpsd & KML*
   * **Android (F-Droid / Open Source):** *gpsdRelay* or *GPSd Forwarder*
   * **iPhone (App Store):** *GPS2IP* (or *GPS2IP Lite*)
2. Connect your phone to your Orbic's Wi-Fi hotspot.
3. Add a new **NMEA** stream in the app:
   * **Protocol:** `UDP Client` (or `TCP Client`)
   * **Host / IP:** `192.168.1.1`
   * **Port:** `10110`
4. Start streaming — the **`GPS: No Fix`** badge on `http://192.168.1.1:8090` will turn **green** within seconds, and `GPS-TAGGED` observations will begin recording.

### Exporting & Uploading Wardriving Sessions
From `http://192.168.1.1:8090`, you can either **download files straight to your phone** or configure **one-click direct uploads** in the **Integrations & Settings** tab:
* **Download WiGLE v1.6 CSV** — Contains GPS-tagged `WIFI`, `BLE`, and `LTE`/`GSM`/`NR` observations in a single spec-compliant `WigleWifi-1.6` file.
* **Download GeoJSON** — Ready for QGIS, geojson.io, uMap, and community mapping tools.
* **WiGLE (`wigle.net`)** — Paste your *Encoded API Token* from `wigle.net/account`.
* **Watch Dogs Go Wars (`wdgwars.pl`)** — Paste your profile upload token.
* **Wardrift (`wardrift.net`)** — Enter your Wardrift username & password (stored locally in `/data/rayscout/config.json`, never echoed back to the browser).

---

## Optional: Flashing the Seeed XIAO ESP32-S3 / Wio-SX1262 Companion

Adding a XIAO ESP32-S3 takes ~2 minutes and unlocks **BLE 5.0**, **2.4 GHz Wi-Fi probe requests**, **deauth flood detection**, and (on the Wio-SX1262 kit) **915 MHz LoRa**.

### 1. Configure Your Orbic Wi-Fi Credentials
Copy `xiao-sensor/include/secrets.example.h` to **`xiao-sensor/include/secrets.h`** (gitignored) and enter your Orbic's 2.4 GHz hotspot SSID & passphrase:
```cpp
#pragma once
#define ORBIC_WIFI_SSID "Your-Orbic-SSID"
#define ORBIC_WIFI_PASS "Your-Orbic-Password"
```
*(Ensure your Orbic Wi-Fi AP is set to 2.4 GHz, as the ESP32-S3 is a 2.4 GHz Wi-Fi radio.)*

### 2. Flash with PlatformIO
Install PlatformIO (`pip install --user platformio`), plug in your XIAO board via USB-C, and run:
```powershell
cd xiao-sensor

# For the bare Seeed Studio XIAO ESP32-S3:
python -m platformio run -e xiao_esp32s3 -t upload

# For the Seeed Studio Wio-SX1262 + XIAO ESP32-S3 kit:
python -m platformio run -e xiao_esp32s3_sx1262 -t upload
```

**Bootloader tip (if `esptool` cannot auto-reset):**
1. **Hold** the tiny **`B` (BOOT)** button on the XIAO.
2. While holding `B`, **tap `R` (RESET)** (or unplug/replug USB-C), then release `B`.
3. Run the upload command above, and **tap `R` once** after flashing completes to boot into RayScout!

### Hardware Pinout Reference (Wio-SX1262 + XIAO ESP32-S3)
| Subsystem | Pins (ESP32-S3 GPIO) | Notes |
| :--- | :--- | :--- |
| **Wio-SX1262 SPI** | `SCK=7`, `MISO=8`, `MOSI=9` | Native B2B connector |
| **Wio-SX1262 Control** | `CS=41`, `BUSY=40`, `DIO1=39`, `RST=42` | 906.875 MHz US default, 1.8V TCXO, DIO2 RF switch |
| **Optional UART GPS** | `TX=43`, `RX=44` (9600 baud) | Free XIAO UART pins for Seeed L76K / BN-220 / ATGM336H |

---

## Customizing Detection Rules (`rules.json`)

You can edit passive signature rules live from the **Signature Rules (JSON)** tab in the Web UI (no restart required). Rules support matching by:
* `radio_types`: `WIFI`, `BLE`, `WIFI_PROBE`, `LTE`, `GSM`, `LORA`
* `ouis`: 3-octet MAC prefixes (e.g., `"60:60:1F"`)
* `name_substrings`: Case-insensitive SSID or BLE name substrings
* `vendor_ies`: 802.11 Beacon Vendor-Specific IE OUIs (e.g., `"FA:0B:BC"` for ASTM F3411 Drone Remote ID)
* `mfg_ids`: BLE Company IDs (e.g., `"0x004C"`)
* `service_uuids`: BLE GATT Service UUIDs (e.g., `"FFFA"`, `"FEED"`, `"FD5A"`)

> **Tip:** If you are in a crowded area with lots of Apple devices, remove `"mfg_ids": ["0x004C"]` from the `ble-tracker-findmy` rule in the Web UI to keep alerts quiet and high-signal.

---

## Local Desktop Demo Mode

Want to preview the UI, simulated Co-Moving alerts, Remote ID hits, and WiGLE CSV export on Windows without an Orbic?
```powershell
.\scripts\build-orbic.ps1 -IncludeWindows
.\Launch-Demo.bat
```
Then open **`http://localhost:8090`**.

---

## Ethical & Passive-Only Notice

RayScout is strictly a **passive** radio-frequency awareness, security research, and wardriving logging tool. It only listens to publicly broadcast beacons, advertisements, and cellular control-plane telemetry — it does not jam, deauthenticate, intercept private traffic, or connect to unauthorized networks. Use responsibly and in compliance with local laws.
