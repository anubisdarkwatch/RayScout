package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"

	"rayscout/internal/model"
)

// BuildWiGLECSV generates a spec-compliant WiGLE CSV v1.6 file containing all
// GPS-tagged WIFI, BLE, and Cellular (LTE/GSM/WCDMA/NR) observations.
// Returns the CSV bytes and the number of data rows written.
func BuildWiGLECSV(obs []model.Observation, deviceName string) ([]byte, int, error) {
	if deviceName == "" {
		deviceName = "RayScout-Orbic-RC400L"
	}

	var buf bytes.Buffer
	// Line 1: WiGLE v1.6 Meta-Header
	fmt.Fprintf(&buf, "WigleWifi-1.6,appRelease=RayScout-0.1,model=Orbic-RC400L,release=Linux-3.18,device=%s,display=128x128,board=MDM9207,brand=Orbic,star=Sol,body=3,subBody=0\n",
		sanitizeHeaderVal(deviceName))

	w := csv.NewWriter(&buf)
	// Line 2: WiGLE v1.6 Column Names
	if err := w.Write([]string{
		"MAC", "SSID", "AuthMode", "FirstSeen", "Channel", "Frequency", "RSSI",
		"CurrentLatitude", "CurrentLongitude", "AltitudeMeters", "AccuracyMeters",
		"RCOIs", "MfgrId", "Type",
	}); err != nil {
		return nil, 0, err
	}

	count := 0
	for _, o := range obs {
		wigleType, ok := mapToWiGLEType(o.Type)
		if !ok {
			continue
		}
		if o.GPS == nil || !o.GPS.Valid || (o.GPS.Latitude == 0 && o.GPS.Longitude == 0) {
			continue
		}

		rssi := o.BestRSSI
		if rssi == 0 {
			rssi = o.RSSI
		}
		mfgr := strings.ReplaceAll(o.OUI, ":", "")
		if o.ManufacturerID != "" {
			mfgr = o.ManufacturerID
		}

		row := []string{
			sanitizeCSVCell(o.ID),
			sanitizeCSVCell(o.Name),
			sanitizeCSVCell(o.AuthMode),
			o.FirstSeen.UTC().Format("2006-01-02 15:04:05"),
			fmt.Sprintf("%d", o.Channel),
			fmt.Sprintf("%d", o.Frequency),
			fmt.Sprintf("%d", rssi),
			fmt.Sprintf("%.7f", o.GPS.Latitude),
			fmt.Sprintf("%.7f", o.GPS.Longitude),
			fmt.Sprintf("%.1f", o.GPS.Altitude),
			fmt.Sprintf("%.1f", o.GPS.Accuracy),
			"",
			sanitizeCSVCell(mfgr),
			wigleType,
		}
		if err := w.Write(row); err != nil {
			return nil, count, err
		}
		count++
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, count, err
	}
	return buf.Bytes(), count, nil
}

// BuildGeoJSON exports all GPS-tagged observations and alerts as a GeoJSON FeatureCollection
// (compatible with QGIS, geojson.io, uMap, and community wardriving tools).
func BuildGeoJSON(obs []model.Observation, alerts []model.Alert) ([]byte, error) {
	type feature struct {
		Type       string         `json:"type"`
		Geometry   map[string]any `json:"geometry"`
		Properties map[string]any `json:"properties"`
	}
	fc := struct {
		Type     string    `json:"type"`
		Features []feature `json:"features"`
	}{
		Type:     "FeatureCollection",
		Features: make([]feature, 0, len(obs)+len(alerts)),
	}

	for _, o := range obs {
		if o.GPS == nil || !o.GPS.Valid {
			continue
		}
		fc.Features = append(fc.Features, feature{
			Type: "Feature",
			Geometry: map[string]any{
				"type":        "Point",
				"coordinates": []float64{o.GPS.Longitude, o.GPS.Latitude, o.GPS.Altitude},
			},
			Properties: map[string]any{
				"kind":       "observation",
				"radio_type": o.Type,
				"id":         o.ID,
				"name":       o.Name,
				"auth_mode":  o.AuthMode,
				"channel":    o.Channel,
				"frequency":  o.Frequency,
				"rssi":       o.BestRSSI,
				"seen_count": o.SeenCount,
				"first_seen": o.FirstSeen,
				"last_seen":  o.LastSeen,
				"source":     o.Source,
				"vendor_ies": o.VendorIEs,
			},
		})
	}

	for _, a := range alerts {
		if a.GPS == nil || !a.GPS.Valid {
			continue
		}
		fc.Features = append(fc.Features, feature{
			Type: "Feature",
			Geometry: map[string]any{
				"type":        "Point",
				"coordinates": []float64{a.GPS.Longitude, a.GPS.Latitude, a.GPS.Altitude},
			},
			Properties: map[string]any{
				"kind":      "alert",
				"severity":  a.Severity,
				"category":  a.Category,
				"title":     a.Title,
				"detail":    a.Detail,
				"device_id": a.DeviceID,
				"timestamp": a.Timestamp,
			},
		})
	}

	return json.MarshalIndent(fc, "", "  ")
}

func mapToWiGLEType(rt model.RadioType) (string, bool) {
	switch rt {
	case model.RadioWiFi:
		return "WIFI", true
	case model.RadioBLE:
		return "BLE", true
	case model.RadioLTE:
		return "LTE", true
	case model.RadioGSM:
		return "GSM", true
	case model.RadioWCDMA:
		return "WCDMA", true
	case model.RadioNR:
		return "NR", true
	default:
		return "", false
	}
}

// sanitizeCSVCell guards against spreadsheet formula injection per WiGLE v1.6 practice.
func sanitizeCSVCell(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 0 {
		switch s[0] {
		case '=', '+', '-', '@':
			return "'" + s
		}
	}
	return s
}

func sanitizeHeaderVal(s string) string {
	s = strings.ReplaceAll(s, ",", "_")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}
