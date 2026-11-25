#pragma once

#if __has_include("secrets.h")
#include "secrets.h"
#endif

// --- Orbic RC400L Hotspot Connection ---
// Set these to your Orbic hotspot Wi-Fi SSID and passphrase.
#ifndef ORBIC_WIFI_SSID
#define ORBIC_WIFI_SSID "Orbic-Speed-4G"
#endif

#ifndef ORBIC_WIFI_PASS
#define ORBIC_WIFI_PASS "change-me-passphrase"
#endif

#ifndef ORBIC_HUB_URL
#define ORBIC_HUB_URL "http://192.168.1.1:8090/api/v1/ingest"
#endif

#ifndef NODE_ID
#define NODE_ID "xiao-s3-sensor"
#endif

// --- Scan Timing (Time-sliced for 2.4 GHz BLE + Wi-Fi coexistence) ---
#define BLE_SCAN_SECONDS      4
#define WIFI_SNIFF_MS         3000
#define WIFI_CONNECT_TMO_MS   7000
#define MAX_BATCH_OBS         64

// --- Optional External UART GPS Module (e.g. Seeed L76K / BN-220 / ATGM336H) ---
// Uses free XIAO ESP32-S3 hardware UART pins (does not conflict with Wio-SX1262 B2B pins).
#define GPS_RX_PIN            44
#define GPS_TX_PIN            43
#define GPS_BAUD              9600

// --- Seeed Studio Wio-SX1262 + XIAO ESP32-S3 B2B Pinout ---
#define LORA_SCK_PIN          7
#define LORA_MISO_PIN         8
#define LORA_MOSI_PIN         9
#define LORA_CS_PIN           41
#define LORA_BUSY_PIN         40
#define LORA_DIO1_PIN         39
#define LORA_RST_PIN          42

// US 915 MHz ISM band defaults (matches Meshtastic US LongFast frequency 906.875 MHz)
#define LORA_FREQ_MHZ         906.875f
#define LORA_BW_KHZ           250.0f
#define LORA_SF               11
#define LORA_CR               5
#define LORA_SYNC_WORD        0x2B
#define LORA_TX_POWER_DBM     20
#define LORA_PREAMBLE_LEN     16
#define LORA_TCXO_VOLTAGE     1.8f
