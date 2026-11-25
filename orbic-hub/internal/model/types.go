package model

import "time"

// RadioType matches WiGLE v1.6 types plus extended sensor telemetry types.
type RadioType string

const (
	RadioWiFi      RadioType = "WIFI"
	RadioBLE       RadioType = "BLE"
	RadioLTE       RadioType = "LTE"
	RadioGSM       RadioType = "GSM"
	RadioWCDMA     RadioType = "WCDMA"
	RadioNR        RadioType = "NR"
	RadioWiFiProbe RadioType = "WIFI_PROBE"
	RadioLoRa      RadioType = "LORA"
)

// GPSFix represents the latest geographic position from any GPS source.
type GPSFix struct {
	Latitude  float64   `json:"lat"`
	Longitude float64   `json:"lon"`
	Altitude  float64   `json:"alt"`
	Accuracy  float64   `json:"acc"`
	Speed     float64   `json:"speed,omitempty"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Valid     bool      `json:"valid"`
}

// Observation is a normalized wireless sighting from Orbic or XIAO ESP32-S3.
type Observation struct {
	Type           RadioType `json:"type"`
	ID             string    `json:"id"`                       // BSSID, BLE MAC, Cell key (MCC_MNC_TAC_CID), or LoRa node/packet ID
	Name           string    `json:"name"`                     // SSID, BLE device name, or carrier/operator name
	AuthMode       string    `json:"auth_mode"`                // WiGLE AuthMode e.g. [WPA2-PSK-CCMP][ESS], [BLE], LTE
	Channel        int       `json:"channel"`                  // Wi-Fi/BLE channel or 0 for cellular
	Frequency      int       `json:"frequency"`                // MHz (e.g. 2437, 5180, 2402, 915)
	RSSI           int       `json:"rssi"`                     // dBm
	OUI            string    `json:"oui,omitempty"`            // First 3 octets uppercase e.g. "AA:BB:CC"
	VendorIEs      []string  `json:"vendor_ies,omitempty"`     // Hex OUI tags seen in beacon vendor IEs e.g. "FA:0B:BC"
	ManufacturerID string    `json:"mfg_id,omitempty"`         // BLE company ID hex e.g. "0x004C"
	ServiceUUIDs   []string  `json:"service_uuids,omitempty"`  // BLE GATT service UUIDs
	CellMCC        int       `json:"cell_mcc,omitempty"`
	CellMNC        int       `json:"cell_mnc,omitempty"`
	CellTAC        int       `json:"cell_tac,omitempty"`
	CellCID        int       `json:"cell_cid,omitempty"`
	CellPCI        int       `json:"cell_pci,omitempty"`
	NeighborCount  int       `json:"neighbor_count,omitempty"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	SeenCount      int       `json:"seen_count"`
	BestRSSI       int       `json:"best_rssi"`
	GPS            *GPSFix   `json:"gps,omitempty"`
	Source         string    `json:"source"` // "orbic_wifi", "orbic_cell", "xiao_ble", "xiao_probe", "xiao_lora"
}

// Severity classifies detection alert priority.
type Severity string

const (
	SeverityInfo Severity = "info"
	SeverityWarn Severity = "warn"
	SeverityHigh Severity = "high"
)

// Alert represents a real-time detection event.
type Alert struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Severity    Severity  `json:"severity"`
	Category    string    `json:"category"` // co_moving, cell_anomaly, signature, remote_id, deauth, rayhunter
	Title       string    `json:"title"`
	Detail      string    `json:"detail"`
	DeviceID    string    `json:"device_id,omitempty"`
	GPS         *GPSFix   `json:"gps,omitempty"`
	Acked       bool      `json:"acked"`
	RelayedLoRa bool      `json:"relayed_lora"`
}

// CompanionIngestBatch is posted by the XIAO ESP32-S3 (+ optional Wio-SX1262).
type CompanionIngestBatch struct {
	NodeID       string        `json:"node_id"`
	HasLoRa      bool          `json:"has_lora"`
	HasGPS       bool          `json:"has_gps"`
	UptimeSec    int64         `json:"uptime_sec"`
	DeauthFrames int           `json:"deauth_frames"`
	GPS          *GPSFix       `json:"gps,omitempty"`
	Observations []Observation `json:"observations"`
}

// CompanionStatus tracks the health of connected XIAO companion node(s).
type CompanionStatus struct {
	Connected       bool      `json:"connected"`
	NodeID          string    `json:"node_id"`
	HasLoRa         bool      `json:"has_lora"`
	HasGPS          bool      `json:"has_gps"`
	LastSeen        time.Time `json:"last_seen"`
	TotalBatches    int64     `json:"total_batches"`
	TotalDeauths    int64     `json:"total_deauths"`
	LastRemoteIP    string    `json:"last_remote_ip"`
}

// CompanionIngestResponse returns pending high-priority LoRa relay messages to Wio-SX1262.
type CompanionIngestResponse struct {
	Status     string   `json:"status"`
	Accepted   int      `json:"accepted"`
	LoRaAlerts []string `json:"lora_alerts,omitempty"`
}

// RayhunterStatus summarizes health and live heuristic analysis polled from local EFF Rayhunter daemon.
type RayhunterStatus struct {
	Reachable       bool      `json:"reachable"`
	URL             string    `json:"url"`
	Version         string    `json:"version,omitempty"`
	ActiveRecording bool      `json:"active_recording"`
	CurrentCapture  string    `json:"current_capture,omitempty"`
	CaptureBytes    int64     `json:"capture_bytes,omitempty"`
	LastMessageTime string    `json:"last_message_time,omitempty"`
	AnalyzedPackets int       `json:"analyzed_packets,omitempty"`
	TotalCaptures   int       `json:"total_captures"`
	WarningCount    int       `json:"warning_count"`
	HighWarnings    int       `json:"high_warnings"`
	MediumWarnings  int       `json:"medium_warnings"`
	LowWarnings     int       `json:"low_warnings"`
	LatestWarning   string    `json:"latest_warning,omitempty"`
	BatteryLevel    int       `json:"battery_level"`
	BatteryPlugged  bool      `json:"battery_plugged"`
	DiskUsedPercent string    `json:"disk_used_percent,omitempty"`
	LastChecked     time.Time `json:"last_checked"`
	LastError       string    `json:"last_error,omitempty"`
}
