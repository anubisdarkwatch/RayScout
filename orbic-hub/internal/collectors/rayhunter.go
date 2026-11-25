package collectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"rayscout/internal/model"
)

// RayhunterBridge polls EFF Rayhunter's local HTTP API (default http://127.0.0.1:8080)
// without touching /dev/diag, surfacing Rayhunter health, battery, capture progress,
// and live NDJSON analysis warnings in RayScout + LoRa.
type RayhunterBridge struct {
	mu           sync.RWMutex
	baseURL      string
	status       model.RayhunterStatus
	lastWarnings int
	client       *http.Client
	onAlert      func(model.Alert)
}

func NewRayhunterBridge(baseURL string, onAlert func(model.Alert)) *RayhunterBridge {
	clean := strings.TrimRight(baseURL, "/")
	return &RayhunterBridge{
		baseURL: clean,
		status: model.RayhunterStatus{
			URL:          clean,
			BatteryLevel: -1,
		},
		client:  &http.Client{Timeout: 4 * time.Second},
		onAlert: onAlert,
	}
}

func (r *RayhunterBridge) Status() model.RayhunterStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *RayhunterBridge) Run(ctx context.Context) {
	if r.baseURL == "" {
		return
	}
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	r.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pollOnce(ctx)
		}
	}
}

func (r *RayhunterBridge) pollOnce(ctx context.Context) {
	now := time.Now().UTC()
	st := model.RayhunterStatus{
		URL:          r.baseURL,
		BatteryLevel: -1,
		LastChecked:  now,
	}

	// 1) Fetch /api/qmdl-manifest
	manifestBody, err := r.get(ctx, "/api/qmdl-manifest", 512*1024)
	if err != nil {
		st.LastError = "Rayhunter unreachable"
		r.setStatus(st)
		return
	}
	st.Reachable = true

	var manifest struct {
		CurrentEntry *struct {
			Name            string `json:"name"`
			LastMessageTime string `json:"last_message_time"`
			QMDLSizeBytes   int64  `json:"qmdl_size_bytes"`
			Version         string `json:"rayhunter_version"`
		} `json:"current_entry"`
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(manifestBody, &manifest); err == nil {
		st.TotalCaptures = len(manifest.Entries)
		if manifest.CurrentEntry != nil {
			st.ActiveRecording = true
			st.TotalCaptures++
			st.CurrentCapture = manifest.CurrentEntry.Name
			st.CaptureBytes = manifest.CurrentEntry.QMDLSizeBytes
			st.LastMessageTime = manifest.CurrentEntry.LastMessageTime
			st.Version = manifest.CurrentEntry.Version
		}
	}

	// 2) Fetch /api/system-stats (battery, disk, version)
	if statsBody, err := r.get(ctx, "/api/system-stats", 64*1024); err == nil {
		var sys struct {
			DiskStats struct {
				UsedPercent string `json:"used_percent"`
			} `json:"disk_stats"`
			RuntimeMetadata struct {
				Version string `json:"rayhunter_version"`
			} `json:"runtime_metadata"`
			BatteryStatus struct {
				Level       int  `json:"level"`
				IsPluggedIn bool `json:"is_plugged_in"`
			} `json:"battery_status"`
		}
		if err := json.Unmarshal(statsBody, &sys); err == nil {
			st.DiskUsedPercent = sys.DiskStats.UsedPercent
			if sys.RuntimeMetadata.Version != "" {
				st.Version = sys.RuntimeMetadata.Version
			}
			st.BatteryLevel = sys.BatteryStatus.Level
			st.BatteryPlugged = sys.BatteryStatus.IsPluggedIn
		}
	}

	// 3) Fetch /api/analysis-report/<current_entry> (NDJSON live heuristic warnings)
	if st.CurrentCapture != "" {
		if reportBody, err := r.get(ctx, "/api/analysis-report/"+st.CurrentCapture, 1024*1024); err == nil {
			parseAnalysisReport(reportBody, &st)
		}
	}

	r.mu.Lock()
	prevWarnings := r.lastWarnings
	if st.WarningCount > prevWarnings && r.onAlert != nil {
		delta := st.WarningCount - prevWarnings
		r.lastWarnings = st.WarningCount
		r.status = st
		r.mu.Unlock()

		sev := model.SeverityWarn
		if st.HighWarnings > 0 {
			sev = model.SeverityHigh
		}
		detail := fmt.Sprintf("Rayhunter detected %d new cellular warning(s) (High: %d, Med: %d, Low: %d).", delta, st.HighWarnings, st.MediumWarnings, st.LowWarnings)
		if st.LatestWarning != "" {
			detail += " Latest: " + st.LatestWarning
		}
		r.onAlert(model.Alert{
			ID:        fmt.Sprintf("rh-%d", now.UnixNano()),
			Timestamp: now,
			Severity:  sev,
			Category:  "rayhunter",
			Title:     "EFF Rayhunter Cellular Warning",
			Detail:    detail,
		})
		return
	}
	r.lastWarnings = st.WarningCount
	r.status = st
	r.mu.Unlock()
}

func parseAnalysisReport(ndjson []byte, st *model.RayhunterStatus) {
	sc := bufio.NewScanner(bytes.NewReader(ndjson))
	sc.Buffer(make([]byte, 64*1024), 512*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // Header line with analyzers list
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		st.AnalyzedPackets++

		var row struct {
			Events []*struct {
				EventType string `json:"event_type"`
				Message   string `json:"message"`
			} `json:"events"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		for _, ev := range row.Events {
			if ev == nil {
				continue
			}
			switch strings.ToLower(ev.EventType) {
			case "high":
				st.HighWarnings++
				st.WarningCount++
				if ev.Message != "" {
					st.LatestWarning = ev.Message
				}
			case "medium":
				st.MediumWarnings++
				st.WarningCount++
				if ev.Message != "" {
					st.LatestWarning = ev.Message
				}
			case "low":
				st.LowWarnings++
				st.WarningCount++
				if ev.Message != "" {
					st.LatestWarning = ev.Message
				}
			}
		}
	}
}

func (r *RayhunterBridge) get(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

func (r *RayhunterBridge) setStatus(st model.RayhunterStatus) {
	r.mu.Lock()
	r.status = st
	r.mu.Unlock()
}
