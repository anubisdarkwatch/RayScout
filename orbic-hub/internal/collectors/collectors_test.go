package collectors

import (
	"testing"
	"time"

	"rayscout/internal/model"
)

func TestParseIwScanWithRemoteID(t *testing.T) {
	sample := `BSS 60:60:1f:aa:bb:cc(on wlan0)
	freq: 2437
	capability: ESS (0x0401)
	signal: -64.00 dBm
	SSID: RID-DRONE-01
	Vendor specific: OUI fa:0b:bc, data: 0d 00 12 34
BSS aa:bb:cc:dd:ee:ff(on wlan0)
	freq: 5180
	capability: ESS Privacy (0x0411)
	signal: -55.00 dBm
	SSID: Home5G
	RSN:	 * Version: 1
`
	now := time.Now().UTC()
	obs := ParseIwScan(sample, now)
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obs))
	}
	if obs[0].Channel != 6 || obs[0].AuthMode != "[OPEN][ESS]" {
		t.Fatalf("unexpected drone obs: %+v", obs[0])
	}
	if len(obs[0].VendorIEs) != 1 || obs[0].VendorIEs[0] != "FA:0B:BC" {
		t.Fatalf("expected Remote ID vendor IE FA:0B:BC, got %+v", obs[0].VendorIEs)
	}
	if obs[1].Channel != 36 || obs[1].AuthMode != "[WPA2-PSK-CCMP][ESS]" {
		t.Fatalf("unexpected 5GHz obs: %+v", obs[1])
	}
}

func TestParseATCellResponse(t *testing.T) {
	resp := `
+COPS: 0,0,"Verizon",7
+CEREG: 2,1,"0412","000641A0",7
+CSQ: 20,99
OK
`
	obs, ok := ParseATCellResponse(resp, time.Now().UTC())
	if !ok {
		t.Fatal("expected valid cellular observation")
	}
	if obs.Name != "Verizon" || obs.CellTAC != 0x0412 || obs.CellCID != 0x000641A0 {
		t.Fatalf("unexpected cell obs: %+v", obs)
	}
	if obs.RSSI != -73 {
		t.Fatalf("expected RSSI -73, got %d", obs.RSSI)
	}
}

func TestParseNMEASentence(t *testing.T) {
	gga := "$GPGGA,123519,3016.0320,N,09744.5860,W,1,08,0.9,165.4,M,46.9,M,,*47"
	fix, ok := ParseNMEASentence(gga, "nmea_tcp")
	if !ok || !fix.Valid {
		t.Fatal("expected valid GGA fix")
	}
	if fix.Latitude < 30.26 || fix.Latitude > 30.27 {
		t.Fatalf("unexpected latitude: %f", fix.Latitude)
	}
	if fix.Longitude > -97.74 || fix.Longitude < -97.75 {
		t.Fatalf("unexpected longitude: %f", fix.Longitude)
	}
}

func TestParseRayhunterAnalysisReport(t *testing.T) {
	ndjson := `{"analyzers":[],"report_version":2}
{"packet_timestamp":"2026-11-25T16:45:52Z","events":[null]}
{"packet_timestamp":"2026-11-25T16:45:55Z","events":[{"event_type":"High","message":"2G downgrade detected"},{"event_type":"Medium","message":"Suspicious identity request"}]}
`
	var st model.RayhunterStatus
	parseAnalysisReport([]byte(ndjson), &st)
	if st.AnalyzedPackets != 2 {
		t.Fatalf("expected 2 analyzed packets, got %d", st.AnalyzedPackets)
	}
	if st.HighWarnings != 1 || st.MediumWarnings != 1 || st.WarningCount != 2 {
		t.Fatalf("unexpected warning counts: %+v", st)
	}
	if st.LatestWarning == "" {
		t.Fatal("expected LatestWarning to be populated")
	}
}
