package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rayscout/internal/collectors"
	"rayscout/internal/config"
	"rayscout/internal/detect"
	"rayscout/internal/model"
)

func TestServerIngestLoRaRelayAndWiGLEExport(t *testing.T) {
	cfg := config.Default()
	cfg.RulesPath = ""
	cfg.CellBaselinePath = ""
	cfg.DataDir = t.TempDir()

	gps := collectors.NewGPSManager("", false)
	eng := detect.NewEngine(cfg, nil)
	rh := collectors.NewRayhunterBridge("", nil)
	srv := New(cfg, "", eng, gps, rh, true)
	h := srv.Handler()

	// 1. Simulate XIAO ESP32-S3 + Wio-SX1262 batch ingest with GPS + BLE + Deauth flood.
	batch := model.CompanionIngestBatch{
		NodeID:       "xiao-s3-sx1262",
		HasLoRa:      true,
		HasGPS:       true,
		DeauthFrames: 20,
		GPS: &model.GPSFix{
			Latitude:  30.2680,
			Longitude: -97.7420,
			Altitude:  166.0,
			Accuracy:  4.0,
			Valid:     true,
		},
		Observations: []model.Observation{
			{
				Type:      model.RadioBLE,
				ID:        "AA:BB:CC:11:22:33",
				Name:      "TestBLE",
				AuthMode:  "[BLE]",
				Channel:   37,
				Frequency: 2402,
				RSSI:      -70,
				Source:    "xiao_ble",
			},
		},
	}
	body, _ := json.Marshal(batch)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var ingResp model.CompanionIngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ingResp); err != nil {
		t.Fatalf("decode ingest resp: %v", err)
	}
	if ingResp.Accepted != 1 || len(ingResp.LoRaAlerts) == 0 {
		t.Fatalf("expected 1 accepted obs and >=1 LoRa deauth relay alert, got %+v", ingResp)
	}

	// 2. Verify embedded Web UI serves cleanly.
	reqUI := httptest.NewRequest(http.MethodGet, "/", nil)
	recUI := httptest.NewRecorder()
	h.ServeHTTP(recUI, reqUI)
	if recUI.Code != http.StatusOK || !strings.Contains(recUI.Body.String(), "RAYSCOUT") {
		t.Fatalf("expected Web UI HTML, got HTTP %d", recUI.Code)
	}

	// 3. Verify WiGLE v1.6 CSV export includes the GPS-tagged BLE row.
	reqCSV := httptest.NewRequest(http.MethodGet, "/api/v1/export/wigle.csv", nil)
	recCSV := httptest.NewRecorder()
	h.ServeHTTP(recCSV, reqCSV)
	if recCSV.Code != http.StatusOK {
		t.Fatalf("wigle export HTTP %d", recCSV.Code)
	}
	csvOut := recCSV.Body.String()
	if !strings.HasPrefix(csvOut, "WigleWifi-1.6,") || !strings.Contains(csvOut, "AA:BB:CC:11:22:33") {
		t.Fatalf("unexpected WiGLE CSV:\n%s", csvOut)
	}
}
