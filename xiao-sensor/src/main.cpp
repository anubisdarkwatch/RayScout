#include <Arduino.h>
#include <WiFi.h>
#include <HTTPClient.h>
#include <esp_wifi.h>
#include <NimBLEDevice.h>
#include <ArduinoJson.h>
#include <TinyGPSPlus.h>
#include <vector>

#include "config.h"

#ifdef ENABLE_LORA_SX1262
#include <SPI.h>
#include <RadioLib.h>
SX1262 loraRadio = new Module(LORA_CS_PIN, LORA_DIO1_PIN, LORA_RST_PIN, LORA_BUSY_PIN);
volatile bool loraRxFlag = false;
bool loraReady = false;

#if defined(ESP32)
ICACHE_RAM_ATTR
#endif
void onLoRaRx() {
  loraRxFlag = true;
}
#endif

struct SensorObs {
  String type;          // "BLE", "WIFI_PROBE", "LORA"
  String id;            // MAC or LoRa packet tag
  String name;          // BLE name, probed SSID, or LoRa summary
  String authMode;
  int channel;
  int frequency;
  int rssi;
  String mfgId;
  std::vector<String> serviceUuids;
  String source;
};

struct ProbeRecord {
  uint8_t mac[6];
  char ssid[33];
  int8_t rssi;
  uint8_t ch;
};

#define PROBE_RING_SIZE 48
static ProbeRecord g_probeRing[PROBE_RING_SIZE];
static volatile size_t g_probeHead = 0;

static std::vector<SensorObs> g_batch;
static portMUX_TYPE g_mux = portMUX_INITIALIZER_UNLOCKED;
static volatile uint32_t g_deauthFrames = 0;
static volatile uint8_t g_currentWifiCh = 1;

static TinyGPSPlus g_gps;
static bool g_gpsEverSeen = false;

static String formatMac(const uint8_t *mac) {
  char buf[18];
  snprintf(buf, sizeof(buf), "%02X:%02X:%02X:%02X:%02X:%02X",
           mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
  return String(buf);
}

static void addOrUpdateObs(const SensorObs &item) {
  for (auto &ex : g_batch) {
    if (ex.type == item.type && ex.id == item.id) {
      if (item.rssi > ex.rssi) ex.rssi = item.rssi;
      if (ex.name.isEmpty() && !item.name.isEmpty()) ex.name = item.name;
      return;
    }
  }
  if (g_batch.size() < MAX_BATCH_OBS) {
    g_batch.push_back(item);
  }
}

// --- Phase 1: BLE 5.0 Scanner ---
class RayScoutBLECallbacks : public NimBLEAdvertisedDeviceCallbacks {
  void onResult(NimBLEAdvertisedDevice *dev) override {
    SensorObs o;
    o.type = "BLE";
    o.id = String(dev->getAddress().toString().c_str());
    o.id.toUpperCase();
    o.name = dev->haveName() ? String(dev->getName().c_str()) : "";
    o.authMode = "[BLE]";
    o.channel = 37;
    o.frequency = 2402;
    o.rssi = dev->getRSSI();
    o.source = "xiao_ble";

    if (dev->haveManufacturerData()) {
      std::string md = dev->getManufacturerData();
      if (md.size() >= 2) {
        uint16_t companyId = (uint8_t)md[0] | ((uint16_t)(uint8_t)md[1] << 8);
        char hexBuf[8];
        snprintf(hexBuf, sizeof(hexBuf), "0x%04X", companyId);
        o.mfgId = String(hexBuf);
      }
    }

    int uuidCount = dev->getServiceUUIDCount();
    for (int i = 0; i < uuidCount && i < 4; i++) {
      o.serviceUuids.push_back(String(dev->getServiceUUID(i).toString().c_str()));
    }
    int sdCount = dev->getServiceDataCount();
    for (int i = 0; i < sdCount && i < 4; i++) {
      o.serviceUuids.push_back(String(dev->getServiceDataUUID(i).toString().c_str()));
    }

    addOrUpdateObs(o);
  }
};

static void runBLEScanPhase() {
  NimBLEDevice::init("");
  NimBLEScan *scan = NimBLEDevice::getScan();
  static RayScoutBLECallbacks cb;
  scan->setAdvertisedDeviceCallbacks(&cb, false);
  scan->setActiveScan(true);
  scan->setInterval(45);
  scan->setWindow(30);
  scan->start(BLE_SCAN_SECONDS, false);
  scan->clearResults();
  NimBLEDevice::deinit(true);
}

// --- Phase 2: 2.4 GHz Wi-Fi Promiscuous Probe & Deauth Sniffer ---
static void wifiPromiscuousCB(void *buf, wifi_promiscuous_pkt_type_t type) {
  if (type != WIFI_PKT_MGMT) return;

  const wifi_promiscuous_pkt_t *pkt = (const wifi_promiscuous_pkt_t *)buf;
  const uint8_t *payload = pkt->payload;
  const int len = pkt->rx_ctrl.sig_len;
  if (len < 24) return;

  uint8_t frameType = (payload[0] & 0x0C) >> 2;
  uint8_t subType   = (payload[0] & 0xF0) >> 4;
  if (frameType != 0) return;

  // Deauth (12) or Disassociation (10)
  if (subType == 12 || subType == 10) {
    g_deauthFrames++;
    return;
  }

  // Probe Request (4) — zero heap allocation inside promiscuous callback
  if (subType == 4) {
    const uint8_t *srcMac = payload + 10;
    ProbeRecord rec{};
    memcpy(rec.mac, srcMac, 6);
    rec.rssi = (int8_t)pkt->rx_ctrl.rssi;
    rec.ch = g_currentWifiCh;

    // Tag 0 (SSID) starts at offset 24 in Probe Request frames
    if (len > 26 && payload[24] == 0) {
      uint8_t ssidLen = payload[25];
      if (ssidLen > 0 && ssidLen <= 32 && (26 + ssidLen) <= len) {
        memcpy(rec.ssid, payload + 26, ssidLen);
        rec.ssid[ssidLen] = '\0';
      }
    }

    portENTER_CRITICAL(&g_mux);
    if (g_probeHead < PROBE_RING_SIZE) {
      g_probeRing[g_probeHead++] = rec;
    }
    portEXIT_CRITICAL(&g_mux);
  }
}

static void runWiFiSniffPhase() {
  portENTER_CRITICAL(&g_mux);
  g_probeHead = 0;
  portEXIT_CRITICAL(&g_mux);

  WiFi.mode(WIFI_STA);
  WiFi.disconnect(true);
  delay(50);

  wifi_promiscuous_filter_t filt = {.filter_mask = WIFI_PROMIS_FILTER_MASK_MGMT};
  esp_wifi_set_promiscuous_filter(&filt);
  esp_wifi_set_promiscuous_rx_cb(&wifiPromiscuousCB);
  esp_wifi_set_promiscuous(true);

  static const uint8_t hopChannels[] = {1, 6, 11, 2, 7, 3, 8, 4, 9, 5, 10};
  const uint32_t start = millis();
  size_t idx = 0;
  while ((millis() - start) < WIFI_SNIFF_MS) {
    g_currentWifiCh = hopChannels[idx % sizeof(hopChannels)];
    esp_wifi_set_channel(g_currentWifiCh, WIFI_SECOND_CHAN_NONE);
    idx++;
    delay(250);
  }

  esp_wifi_set_promiscuous(false);
  esp_wifi_set_promiscuous_rx_cb(nullptr);

  // Drain fixed ring buffer into g_batch safely outside the callback.
  for (size_t i = 0; i < g_probeHead; i++) {
    const ProbeRecord &rec = g_probeRing[i];
    SensorObs o;
    o.type = "WIFI_PROBE";
    o.id = formatMac(rec.mac);
    o.name = String(rec.ssid);
    o.authMode = "[PROBE]";
    o.channel = rec.ch;
    o.frequency = 2407 + (rec.ch * 5);
    o.rssi = rec.rssi;
    o.source = "xiao_probe";
    addOrUpdateObs(o);
  }
  g_probeHead = 0;
}

// --- Background UART GPS Drain ---
static void drainGPS() {
  while (Serial1.available() > 0) {
    if (g_gps.encode(Serial1.read())) {
      g_gpsEverSeen = true;
    }
  }
}

// --- Optional Wio-SX1262 LoRa Support ---
#ifdef ENABLE_LORA_SX1262
static void initLoRa() {
  SPI.begin(LORA_SCK_PIN, LORA_MISO_PIN, LORA_MOSI_PIN, LORA_CS_PIN);
  int state = loraRadio.begin(
      LORA_FREQ_MHZ, LORA_BW_KHZ, LORA_SF, LORA_CR,
      LORA_SYNC_WORD, LORA_TX_POWER_DBM, LORA_PREAMBLE_LEN, LORA_TCXO_VOLTAGE);
  if (state == RADIOLIB_ERR_NONE) {
    loraRadio.setDio2AsRfSwitch(true);
    loraRadio.setDio1Action(onLoRaRx);
    loraRadio.startReceive();
    loraReady = true;
    Serial.println("[LoRa] Wio-SX1262 initialized on 906.875 MHz");
  } else {
    Serial.printf("[LoRa] SX1262 init failed (code %d)\n", state);
  }
}

static void pollLoRaPassiveRx() {
  if (!loraReady || !loraRxFlag) return;
  loraRxFlag = false;

  uint8_t buf[64];
  size_t len = loraRadio.getPacketLength();
  if (len > sizeof(buf)) len = sizeof(buf);
  int state = loraRadio.readData(buf, len);
  if (state == RADIOLIB_ERR_NONE && len > 0) {
    char idBuf[24];
    snprintf(idBuf, sizeof(idBuf), "LORA-%02X%02X%02X%02X",
             buf[0], len > 1 ? buf[1] : 0, len > 2 ? buf[2] : 0, len > 3 ? buf[3] : 0);
    SensorObs o;
    o.type = "LORA";
    o.id = String(idBuf);
    o.name = String("LoRa ") + String(len) + "B SNR:" + String(loraRadio.getSNR(), 1);
    o.authMode = "[LORA-915]";
    o.channel = 0;
    o.frequency = (int)LORA_FREQ_MHZ;
    o.rssi = (int)loraRadio.getRSSI();
    o.source = "xiao_lora";
    addOrUpdateObs(o);
  }
  loraRadio.startReceive();
}

static void transmitLoRaAlert(const String &msg) {
  if (!loraReady) return;
  Serial.printf("[LoRa TX] Relaying alert: %s\n", msg.c_str());
  loraRadio.standby();
  String txBuf = msg;
  int state = loraRadio.transmit(txBuf);
  if (state != RADIOLIB_ERR_NONE) {
    Serial.printf("[LoRa TX] Failed (code %d)\n", state);
  }
  loraRadio.startReceive();
}
#endif

// --- Phase 3: Sync JSON Batch to Orbic RC400L Hub ---
static void syncWithOrbicHub() {
  WiFi.mode(WIFI_STA);
  WiFi.begin(ORBIC_WIFI_SSID, ORBIC_WIFI_PASS);

  uint32_t t0 = millis();
  while (WiFi.status() != WL_CONNECTED && (millis() - t0) < WIFI_CONNECT_TMO_MS) {
    drainGPS();
    delay(150);
  }
  if (WiFi.status() != WL_CONNECTED) {
    Serial.println("[Sync] Orbic AP unreachable; retrying next cycle");
    return;
  }

  JsonDocument doc;
  doc["node_id"] = NODE_ID;
#ifdef ENABLE_LORA_SX1262
  doc["has_lora"] = loraReady;
#else
  doc["has_lora"] = false;
#endif
  doc["has_gps"] = g_gpsEverSeen;
  doc["uptime_sec"] = millis() / 1000;
  doc["deauth_frames"] = (uint32_t)g_deauthFrames;

  if (g_gps.location.isValid() && g_gps.location.age() < 10000) {
    JsonObject gpsObj = doc["gps"].to<JsonObject>();
    gpsObj["lat"] = g_gps.location.lat();
    gpsObj["lon"] = g_gps.location.lng();
    gpsObj["alt"] = g_gps.altitude.isValid() ? g_gps.altitude.meters() : 0.0;
    gpsObj["acc"] = g_gps.hdop.isValid() ? (g_gps.hdop.hdop() * 5.0) : 8.0;
    gpsObj["valid"] = true;
    gpsObj["source"] = "xiao_gps";
  }

  JsonArray arr = doc["observations"].to<JsonArray>();
  for (const auto &o : g_batch) {
    JsonObject obj = arr.add<JsonObject>();
    obj["type"] = o.type;
    obj["id"] = o.id;
    obj["name"] = o.name;
    obj["auth_mode"] = o.authMode;
    obj["channel"] = o.channel;
    obj["frequency"] = o.frequency;
    obj["rssi"] = o.rssi;
    obj["source"] = o.source;
    if (!o.mfgId.isEmpty()) obj["mfg_id"] = o.mfgId;
    if (!o.serviceUuids.empty()) {
      JsonArray uuids = obj["service_uuids"].to<JsonArray>();
      for (const auto &u : o.serviceUuids) uuids.add(u);
    }
  }

  String payload;
  serializeJson(doc, payload);

  HTTPClient http;
  http.begin(ORBIC_HUB_URL);
  http.addHeader("Content-Type", "application/json");
  http.setTimeout(4000);
  int code = http.POST(payload);
  if (code >= 200 && code < 300) {
    String respBody = http.getString();
    Serial.printf("[Sync] Sent %u obs, %lu deauths -> HTTP %d\n",
                  (unsigned)g_batch.size(), (unsigned long)g_deauthFrames, code);
    g_batch.clear();
    g_deauthFrames = 0;

#ifdef ENABLE_LORA_SX1262
    JsonDocument respDoc;
    if (deserializeJson(respDoc, respBody) == DeserializationError::Ok) {
      JsonArray alerts = respDoc["lora_alerts"].as<JsonArray>();
      for (JsonVariant v : alerts) {
        transmitLoRaAlert(v.as<String>());
      }
    }
#endif
  } else {
    Serial.printf("[Sync] POST failed: HTTP %d\n", code);
  }
  http.end();
  WiFi.disconnect(true);
}

void setup() {
  Serial.begin(115200);
  delay(800);
  Serial.printf("\n=== RayScout XIAO Sensor (%s) ===\n", NODE_ID);

  // Initialize optional UART GPS on XIAO free pins (RX=44, TX=43).
  Serial1.begin(GPS_BAUD, SERIAL_8N1, GPS_RX_PIN, GPS_TX_PIN);

#ifdef ENABLE_LORA_SX1262
  initLoRa();
#endif
}

void loop() {
  drainGPS();
#ifdef ENABLE_LORA_SX1262
  pollLoRaPassiveRx();
#endif

  // 1) BLE 5.0 scan window
  runBLEScanPhase();
  drainGPS();

  // 2) 2.4 GHz Wi-Fi promiscuous probe + deauth window
  runWiFiSniffPhase();
  drainGPS();

#ifdef ENABLE_LORA_SX1262
  pollLoRaPassiveRx();
#endif

  // 3) Sync batch to Orbic RC400L + receive any LoRa relay alerts
  syncWithOrbicHub();
}
