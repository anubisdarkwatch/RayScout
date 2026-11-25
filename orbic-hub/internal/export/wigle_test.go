package export

import (
	"strings"
	"testing"
	"time"

	"rayscout/internal/model"
)

func TestBuildWiGLECSV(t *testing.T) {
	now := time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC)
	gps := &model.GPSFix{
		Latitude:  30.2672,
		Longitude: -97.7431,
		Altitude:  165.0,
		Accuracy:  5.0,
		Valid:     true,
	}
	obs := []model.Observation{
		{
			Type:      model.RadioWiFi,
			ID:        "AA:BB:CC:DD:EE:FF",
			OUI:       "AA:BB:CC",
			Name:      "=EvilFormulaSSID",
			AuthMode:  "[WPA2-PSK-CCMP][ESS]",
			Channel:   6,
			Frequency: 2437,
			RSSI:      -62,
			BestRSSI:  -62,
			FirstSeen: now,
			GPS:       gps,
		},
		{
			Type:      model.RadioBLE,
			ID:        "11:22:33:44:55:66",
			Name:      "Beacon1",
			AuthMode:  "[BLE]",
			Channel:   37,
			Frequency: 2402,
			RSSI:      -75,
			BestRSSI:  -75,
			FirstSeen: now,
			GPS:       gps,
		},
		{
			Type:      model.RadioLTE,
			ID:        "311480_1042_410000",
			Name:      "Verizon",
			AuthMode:  "LTE",
			RSSI:      -88,
			BestRSSI:  -88,
			FirstSeen: now,
			GPS:       gps,
		},
		{
			// Should be skipped because no GPS fix.
			Type:      model.RadioWiFi,
			ID:        "00:00:00:00:00:01",
			Name:      "NoGPS",
			FirstSeen: now,
		},
	}

	data, rows, err := BuildWiGLECSV(obs, "RayScout-Test")
	if err != nil {
		t.Fatalf("BuildWiGLECSV error: %v", err)
	}
	if rows != 3 {
		t.Fatalf("expected 3 rows, got %d", rows)
	}
	out := string(data)
	if !strings.HasPrefix(out, "WigleWifi-1.6,") {
		t.Fatalf("missing WiGLE 1.6 header: %s", out)
	}
	if !strings.Contains(out, "'=EvilFormulaSSID") {
		t.Fatalf("expected formula escaping for SSID, got:\n%s", out)
	}
	if !strings.Contains(out, ",WIFI\n") || !strings.Contains(out, ",BLE\n") || !strings.Contains(out, ",LTE\n") {
		t.Fatalf("missing expected radio types in CSV:\n%s", out)
	}
}
