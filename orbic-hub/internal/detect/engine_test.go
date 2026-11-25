package detect

import (
	"testing"
	"time"

	"rayscout/internal/config"
	"rayscout/internal/model"
)

func TestCoMovingAndRemoteIDDetection(t *testing.T) {
	cfg := config.Default()
	cfg.RulesPath = ""
	cfg.CellBaselinePath = ""
	cfg.CoMovingMinCells = 3

	eng := NewEngine(cfg, nil)
	err := eng.SaveAndReloadRules("", []byte(`{
		"version":"1.0",
		"rules":[{
			"id":"rid",
			"name":"Drone Remote ID",
			"category":"remote_id",
			"severity":"info",
			"radio_types":["WIFI"],
			"vendor_ies":["FA:0B:BC"]
		}]
	}`))
	if err != nil {
		t.Fatalf("failed loading rules: %v", err)
	}

	now := time.Now().UTC()
	// Move across 3 distinct cell towers while seeing the same mobile AP.
	for i := 1; i <= 3; i++ {
		eng.Ingest(model.Observation{
			Type:      model.RadioLTE,
			ID:        "311480_100_" + string(rune('0'+i)),
			Name:      "Carrier",
			RSSI:      -85,
			FirstSeen: now,
			LastSeen:  now,
		}, nil)
		eng.Ingest(model.Observation{
			Type:      model.RadioWiFi,
			ID:        "DE:AD:BE:EF:00:01",
			Name:      "FollowingHotspot",
			AuthMode:  "[WPA2-PSK-CCMP][ESS]",
			RSSI:      -60,
			FirstSeen: now,
			LastSeen:  now,
		}, nil)
	}

	// Also inject a Remote ID drone beacon.
	eng.Ingest(model.Observation{
		Type:      model.RadioWiFi,
		ID:        "60:60:1F:11:22:33",
		Name:      "RID-1234",
		VendorIEs: []string{"FA:0B:BC"},
		RSSI:      -65,
		FirstSeen: now,
		LastSeen:  now,
	}, nil)

	alerts := eng.Alerts()
	var sawCoMoving, sawRemoteID bool
	for _, a := range alerts {
		if a.Category == "co_moving" {
			sawCoMoving = true
		}
		if a.Category == "remote_id" {
			sawRemoteID = true
		}
	}
	if !sawCoMoving {
		t.Fatalf("expected co_moving alert, got %+v", alerts)
	}
	if !sawRemoteID {
		t.Fatalf("expected remote_id alert, got %+v", alerts)
	}
}
