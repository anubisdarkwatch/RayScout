package collectors

import (
	"context"
	"fmt"
	"math"
	"time"

	"rayscout/internal/model"
)

// DemoCollector simulates a realistic drive with Wi-Fi APs, LTE cell handoffs,
// a Co-Moving hotspot, a Wi-Fi Remote ID drone, and BLE sightings so you can
// test the entire RayScout stack on Windows/Linux without needing live radios.
type DemoCollector struct {
	GPS   *GPSManager
	OnObs func(model.Observation)
}

func (d *DemoCollector) Run(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	step := 0
	emit := func() {
		now := time.Now().UTC()
		// Move ~250m north-east each tick along a simulated route in Austin, TX.
		lat := 30.2672 + float64(step)*0.0022
		lon := -97.7431 + float64(step)*0.0018
		fix := model.GPSFix{
			Latitude:  math.Round(lat*1e6) / 1e6,
			Longitude: math.Round(lon*1e6) / 1e6,
			Altitude:  165.4,
			Accuracy:  4.5,
			Speed:     13.4,
			Source:    "demo_gps",
			Timestamp: now,
			Valid:     true,
		}
		if d.GPS != nil {
			d.GPS.Update(fix)
		}

		// Simulate changing LTE cell towers every 2 ticks.
		cellIdx := (step / 2) % 5
		cid := 410000 + cellIdx*17
		cellObs := model.Observation{
			Type:          model.RadioLTE,
			ID:            fmt.Sprintf("311480_1042_%d", cid),
			Name:          "Verizon",
			AuthMode:      "LTE",
			RSSI:          -82 - (step % 10),
			BestRSSI:      -82,
			CellMCC:       311,
			CellMNC:       480,
			CellTAC:       1042,
			CellCID:       cid,
			CellPCI:       120 + cellIdx*11,
			NeighborCount: 4,
			FirstSeen:     now,
			LastSeen:      now,
			SeenCount:     1,
			Source:        "orbic_cell",
		}
		if step == 7 {
			// Inject a simulated strong unmapped tower with neighbor drop.
			cellObs.ID = "311480_9999_888888"
			cellObs.CellTAC = 9999
			cellObs.CellCID = 888888
			cellObs.RSSI = -62
			cellObs.NeighborCount = 0
		}
		d.OnObs(cellObs)

		// Stationary local Wi-Fi AP unique to each step.
		d.OnObs(model.Observation{
			Type:      model.RadioWiFi,
			ID:        fmt.Sprintf("AA:11:22:33:44:%02X", step%250),
			OUI:       "AA:11:22",
			Name:      fmt.Sprintf("Neighborhood_5G_%d", step),
			AuthMode:  "[WPA2-PSK-CCMP][ESS]",
			Channel:   36,
			Frequency: 5180,
			RSSI:      -68,
			BestRSSI:  -68,
			FirstSeen: now,
			LastSeen:  now,
			SeenCount: 1,
			Source:    "orbic_wifi",
		})

		// Persistent Co-Moving mobile hotspot (triggers alert once seen across >=3 cells).
		d.OnObs(model.Observation{
			Type:      model.RadioWiFi,
			ID:        "DE:AD:BE:EF:01:23",
			OUI:       "DE:AD:BE",
			Name:      "Mobile-Router-4G",
			AuthMode:  "[WPA2-PSK-CCMP][ESS]",
			Channel:   6,
			Frequency: 2437,
			RSSI:      -59,
			BestRSSI:  -59,
			FirstSeen: now,
			LastSeen:  now,
			SeenCount: 1,
			Source:    "orbic_wifi",
		})

		// Periodic Wi-Fi Beacon Remote ID drone sighting.
		if step == 2 || step == 10 {
			d.OnObs(model.Observation{
				Type:      model.RadioWiFi,
				ID:        "60:60:1F:AA:BB:CC",
				OUI:       "60:60:1F",
				Name:      "RID-1581F5FJD23001",
				AuthMode:  "[OPEN][ESS]",
				Channel:   1,
				Frequency: 2412,
				RSSI:      -64,
				BestRSSI:  -64,
				VendorIEs: []string{"FA:0B:BC"},
				FirstSeen: now,
				LastSeen:  now,
				SeenCount: 1,
				Source:    "orbic_wifi",
			})
		}

		// Simulated BLE sighting as if XIAO ESP32-S3 were connected.
		if step%2 == 1 {
			d.OnObs(model.Observation{
				Type:         model.RadioBLE,
				ID:           fmt.Sprintf("F0:99:19:00:01:%02X", step%16),
				OUI:          "F0:99:19",
				Name:         "Tile-Demo-Tag",
				AuthMode:     "[BLE]",
				Channel:      37,
				Frequency:    2402,
				RSSI:         -74,
				BestRSSI:     -74,
				ServiceUUIDs: []string{"FEED"},
				FirstSeen:    now,
				LastSeen:     now,
				SeenCount:    1,
				Source:       "xiao_ble",
			})
		}

		step++
	}

	emit()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emit()
		}
	}
}
