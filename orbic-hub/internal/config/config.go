package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Config holds runtime configuration for the RayScout Orbic Hub.
type Config struct {
	mu sync.RWMutex `json:"-"`

	ListenAddr       string `json:"listen_addr"`
	NMEAListenAddr   string `json:"nmea_listen_addr"` // TCP/UDP NMEA GPS listener (e.g. ":10110")
	DataDir          string `json:"data_dir"`
	RulesPath        string `json:"rules_path"`
	CellBaselinePath string `json:"cell_baseline_path"`

	// Collectors
	WiFiInterface     string `json:"wifi_interface"`
	WiFiScanIntervalS int    `json:"wifi_scan_interval_s"`
	CellATPort        string `json:"cell_at_port"` // Auto-detected if empty
	CellPollIntervalS int    `json:"cell_poll_interval_s"`

	// Rayhunter Integration
	RayhunterURL        string `json:"rayhunter_url"`
	RayhunterForwardGPS bool   `json:"rayhunter_forward_gps"`

	// Detection Thresholds
	CoMovingMinCells     int     `json:"comoving_min_cells"`      // Distinct cell towers before alerting
	CoMovingMinDistM     float64 `json:"comoving_min_dist_m"`     // Minimum GPS travel distance (meters)
	CoMovingMinDurationS int     `json:"comoving_min_duration_s"` // Minimum time span (seconds)
	DeauthAlertThreshold int     `json:"deauth_alert_threshold"`  // Deauth frames per S3 batch to trigger alert

	// Uploaders & Notifications
	WiGLEAuthToken     string   `json:"wigle_auth_token"`     // Encoded API token from wigle.net/account
	WiGLEDonate        bool     `json:"wigle_donate"`
	WDGWarsURL         string   `json:"wdgwars_url"`          // e.g. https://wdgwars.pl/api/upload-csv
	WDGWarsToken       string   `json:"wdgwars_token"`        // Profile API token
	WardriftURL        string   `json:"wardrift_url"`         // e.g. https://wardrift.net
	WardriftLoginPath  string   `json:"wardrift_login_path"`  // e.g. /api/login
	WardriftUploadPath string   `json:"wardrift_upload_path"` // e.g. /api/upload
	WardriftUsername   string   `json:"wardrift_username"`
	WardriftPassword   string   `json:"wardrift_password"`
	NtfyURL            string   `json:"ntfy_url"`             // Optional ntfy.sh topic URL for push alerts
	DeviceName         string   `json:"device_name"`          // Metadata tag written to WiGLE CSV header
	WhitelistMACs      []string `json:"whitelist_macs"`       // Own devices ignored by co-moving detector
}

// Default returns sensible defaults tuned for the Orbic RC400L running Rayhunter.
func Default() *Config {
	return &Config{
		ListenAddr:           ":8090",
		NMEAListenAddr:       ":10110",
		DataDir:              "/data/rayscout/data",
		RulesPath:            "/data/rayscout/rules.json",
		CellBaselinePath:     "/data/rayscout/cell_baseline.csv",
		WiFiInterface:        "wlan0",
		WiFiScanIntervalS:    8,
		CellATPort:           "",
		CellPollIntervalS:    10,
		RayhunterURL:         "http://127.0.0.1:8080",
		RayhunterForwardGPS:  true,
		CoMovingMinCells:     3,
		CoMovingMinDistM:     800,
		CoMovingMinDurationS: 300,
		DeauthAlertThreshold: 15,
		WiGLEDonate:          true,
		WDGWarsURL:           "https://wdgwars.pl/api/upload-csv",
		WardriftURL:          "https://wardrift.net",
		WardriftLoginPath:    "/api/login",
		WardriftUploadPath:   "/api/upload",
		DeviceName:           "RayScout-Orbic-RC400L",
		WhitelistMACs:        []string{},
	}
}

// Load reads config from path, creating defaults if missing.
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the current config atomically to disk.
func (c *Config) Save(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Snapshot returns a thread-safe shallow copy of settings.
func (c *Config) Snapshot() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := *c
	cp.WhitelistMACs = append([]string(nil), c.WhitelistMACs...)
	return cp
}

// Update applies a mutation safely under write lock.
func (c *Config) Update(fn func(*Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

// IsWhitelisted reports whether a normalized MAC address is whitelisted.
func (c *Config) IsWhitelisted(mac string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, w := range c.WhitelistMACs {
		if w == mac {
			return true
		}
	}
	return false
}
