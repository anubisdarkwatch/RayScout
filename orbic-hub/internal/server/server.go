package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rayscout/internal/collectors"
	"rayscout/internal/config"
	"rayscout/internal/detect"
	"rayscout/internal/export"
	"rayscout/internal/model"
)

//go:embed web/index.html
var webFS embed.FS

// Server exposes the RayScout Web UI, Companion S3 ingest API, Phone GPS bridge,
// and WiGLE/WDGWars/GeoJSON exporters.
type Server struct {
	cfg        *config.Config
	cfgPath    string
	engine     *detect.Engine
	gps        *collectors.GPSManager
	rayhunter  *collectors.RayhunterBridge
	demoMode   bool
	startedAt  time.Time
}

func New(cfg *config.Config, cfgPath string, engine *detect.Engine, gps *collectors.GPSManager, rh *collectors.RayhunterBridge, demoMode bool) *Server {
	return &Server{
		cfg:       cfg,
		cfgPath:   cfgPath,
		engine:    engine,
		gps:       gps,
		rayhunter: rh,
		demoMode:  demoMode,
		startedAt: time.Now().UTC(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/observations", s.handleObservations)
	mux.HandleFunc("/api/v1/alerts", s.handleAlerts)
	mux.HandleFunc("/api/v1/alerts/ack", s.handleAckAlert)
	mux.HandleFunc("/api/v1/whitelist", s.handleWhitelist)
	mux.HandleFunc("/api/v1/gps", s.handleGPS)
	mux.HandleFunc("/api/v1/ingest", s.handleIngest)
	mux.HandleFunc("/api/v1/export/wigle.csv", s.handleExportWiGLE)
	mux.HandleFunc("/api/v1/export/session.geojson", s.handleExportGeoJSON)
	mux.HandleFunc("/api/v1/upload/wigle", s.handleUploadWiGLE)
	mux.HandleFunc("/api/v1/upload/wdgwars", s.handleUploadWDGWars)
	mux.HandleFunc("/api/v1/upload/wardrift", s.handleUploadWardrift)
	mux.HandleFunc("/api/v1/rules", s.handleRules)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	return withCORS(mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "UI unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	var currentCell *model.Observation
	for _, rt := range []string{"LTE", "NR", "WCDMA", "GSM"} {
		if list := s.engine.Observations(rt, 1); len(list) > 0 {
			if currentCell == nil || list[0].LastSeen.After(currentCell.LastSeen) {
				cp := list[0]
				currentCell = &cp
			}
		}
	}
	resp := map[string]any{
		"version":      "0.1.0",
		"demo_mode":    s.demoMode,
		"uptime_s":     int64(time.Since(s.startedAt).Seconds()),
		"device_name":  snap.DeviceName,
		"stats":        s.engine.Stats(),
		"gps":          s.gps.Current(),
		"companion":    s.engine.CompanionStatus(),
		"rayhunter":    s.rayhunter.Status(),
		"current_cell": currentCell,
		"integrations": map[string]bool{
			"wigle_configured":    snap.WiGLEAuthToken != "",
			"wdgwars_configured":  snap.WDGWarsToken != "",
			"wardrift_configured": snap.WardriftUsername != "" && snap.WardriftPassword != "",
			"ntfy_configured":     snap.NtfyURL != "",
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleObservations(w http.ResponseWriter, r *http.Request) {
	filterType := r.URL.Query().Get("type")
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 2000 {
			limit = v
		}
	}
	writeJSON(w, http.StatusOK, s.engine.Observations(filterType, limit))
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Alerts())
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ok := s.engine.AckAlert(req.ID)
	writeJSON(w, http.StatusOK, map[string]any{"acked": ok})
}

func (s *Server) handleWhitelist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mac := strings.ToUpper(strings.TrimSpace(req.MAC))
	if mac == "" {
		http.Error(w, "mac required", http.StatusBadRequest)
		return
	}
	s.cfg.Update(func(c *config.Config) {
		for _, existing := range c.WhitelistMACs {
			if existing == mac {
				return
			}
		}
		c.WhitelistMACs = append(c.WhitelistMACs, mac)
	})
	if s.cfgPath != "" {
		_ = s.cfg.Save(s.cfgPath)
	}
	writeJSON(w, http.StatusOK, map[string]any{"whitelisted": mac})
}

func (s *Server) handleGPS(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, s.gps.Current())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	var fix model.GPSFix
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&fix); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if fix.Source == "" {
		fix.Source = "phone_bridge"
	}
	fix.Valid = fix.Latitude != 0 || fix.Longitude != 0
	fix.Timestamp = time.Now().UTC()
	s.gps.Update(fix)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "gps": s.gps.Current()})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var batch model.CompanionIngestBatch
	if err := json.NewDecoder(io.LimitReader(r.Body, 512*1024)).Decode(&batch); err != nil {
		http.Error(w, "invalid ingest json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if batch.GPS != nil && batch.GPS.Valid {
		batch.GPS.Source = "xiao_gps"
		batch.GPS.Timestamp = time.Now().UTC()
		s.gps.Update(*batch.GPS)
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	resp := s.engine.IngestCompanionBatch(batch, host, s.gps.Current())
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleExportWiGLE(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	data, rows, err := export.BuildWiGLECSV(s.engine.Observations("", 0), snap.DeviceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("rayscout_%s.csv", time.Now().UTC().Format("20060102_150405"))
	s.archiveExport(filename, data)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-RayScout-Rows", strconv.Itoa(rows))
	_, _ = w.Write(data)
}

func (s *Server) handleExportGeoJSON(w http.ResponseWriter, r *http.Request) {
	data, err := export.BuildGeoJSON(s.engine.Observations("", 0), s.engine.Alerts())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("rayscout_%s.geojson", time.Now().UTC().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/geo+json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = w.Write(data)
}

func (s *Server) handleUploadWiGLE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	snap := s.cfg.Snapshot()
	data, rows, err := export.BuildWiGLECSV(s.engine.Observations("", 0), snap.DeviceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("rayscout_%s.csv", time.Now().UTC().Format("20060102_150405"))
	res, err := export.UploadWiGLE(r.Context(), data, filename, rows, snap.WiGLEAuthToken, snap.WiGLEDonate)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error(), "result": res})
		return
	}
	s.archiveExport(filename, data)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (s *Server) handleUploadWDGWars(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	snap := s.cfg.Snapshot()
	data, rows, err := export.BuildWiGLECSV(s.engine.Observations("", 0), snap.DeviceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("rayscout_%s.csv", time.Now().UTC().Format("20060102_150405"))
	res, err := export.UploadWDGWars(r.Context(), data, filename, rows, snap.WDGWarsURL, snap.WDGWarsToken)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error(), "result": res})
		return
	}
	s.archiveExport(filename, data)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (s *Server) handleUploadWardrift(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	snap := s.cfg.Snapshot()
	data, rows, err := export.BuildWiGLECSV(s.engine.Observations("", 0), snap.DeviceName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("rayscout_%s.csv", time.Now().UTC().Format("20060102_150405"))
	res, err := export.UploadWardrift(
		r.Context(), data, filename, rows,
		snap.WardriftURL, snap.WardriftLoginPath, snap.WardriftUploadPath,
		snap.WardriftUsername, snap.WardriftPassword,
	)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error(), "result": res})
		return
	}
	s.archiveExport(filename, data)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.engine.Rules())
	case http.MethodPut, http.MethodPost:
		raw, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.engine.SaveAndReloadRules(s.cfg.Snapshot().RulesPath, raw); err != nil {
			http.Error(w, "invalid rules json: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "reloaded", "count": len(s.engine.Rules().Rules)})
	default:
		http.Error(w, "GET or PUT required", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snap := s.cfg.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"device_name":             snap.DeviceName,
			"wifi_interface":          snap.WiFiInterface,
			"rayhunter_url":           snap.RayhunterURL,
			"rayhunter_forward_gps":   snap.RayhunterForwardGPS,
			"comoving_min_cells":      snap.CoMovingMinCells,
			"comoving_min_dist_m":     snap.CoMovingMinDistM,
			"comoving_min_duration_s": snap.CoMovingMinDurationS,
			"deauth_alert_threshold":  snap.DeauthAlertThreshold,
			"wigle_donate":            snap.WiGLEDonate,
			"wigle_configured":        snap.WiGLEAuthToken != "",
			"wdgwars_url":             snap.WDGWarsURL,
			"wdgwars_configured":      snap.WDGWarsToken != "",
			"wardrift_url":            snap.WardriftURL,
			"wardrift_login_path":     snap.WardriftLoginPath,
			"wardrift_upload_path":    snap.WardriftUploadPath,
			"wardrift_username":       snap.WardriftUsername,
			"wardrift_configured":     snap.WardriftUsername != "" && snap.WardriftPassword != "",
			"ntfy_url":                snap.NtfyURL,
			"whitelist_macs":          snap.WhitelistMACs,
		})
	case http.MethodPut, http.MethodPost:
		var patch struct {
			DeviceName           *string  `json:"device_name"`
			WiGLEAuthToken       *string  `json:"wigle_auth_token"`
			WiGLEDonate          *bool    `json:"wigle_donate"`
			WDGWarsURL           *string  `json:"wdgwars_url"`
			WDGWarsToken         *string  `json:"wdgwars_token"`
			WardriftURL          *string  `json:"wardrift_url"`
			WardriftLoginPath    *string  `json:"wardrift_login_path"`
			WardriftUploadPath   *string  `json:"wardrift_upload_path"`
			WardriftUsername     *string  `json:"wardrift_username"`
			WardriftPassword     *string  `json:"wardrift_password"`
			NtfyURL              *string  `json:"ntfy_url"`
			CoMovingMinCells     *int     `json:"comoving_min_cells"`
			CoMovingMinDistM     *float64 `json:"comoving_min_dist_m"`
			DeauthAlertThreshold *int     `json:"deauth_alert_threshold"`
			RayhunterForwardGPS  *bool    `json:"rayhunter_forward_gps"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&patch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.cfg.Update(func(c *config.Config) {
			if patch.DeviceName != nil && *patch.DeviceName != "" {
				c.DeviceName = *patch.DeviceName
			}
			if patch.WiGLEAuthToken != nil && *patch.WiGLEAuthToken != "" {
				c.WiGLEAuthToken = strings.TrimSpace(*patch.WiGLEAuthToken)
			}
			if patch.WiGLEDonate != nil {
				c.WiGLEDonate = *patch.WiGLEDonate
			}
			if patch.WDGWarsURL != nil && *patch.WDGWarsURL != "" {
				c.WDGWarsURL = strings.TrimSpace(*patch.WDGWarsURL)
			}
			if patch.WDGWarsToken != nil && *patch.WDGWarsToken != "" {
				c.WDGWarsToken = strings.TrimSpace(*patch.WDGWarsToken)
			}
			if patch.WardriftURL != nil && *patch.WardriftURL != "" {
				c.WardriftURL = strings.TrimSpace(*patch.WardriftURL)
			}
			if patch.WardriftLoginPath != nil && *patch.WardriftLoginPath != "" {
				c.WardriftLoginPath = strings.TrimSpace(*patch.WardriftLoginPath)
			}
			if patch.WardriftUploadPath != nil && *patch.WardriftUploadPath != "" {
				c.WardriftUploadPath = strings.TrimSpace(*patch.WardriftUploadPath)
			}
			if patch.WardriftUsername != nil {
				c.WardriftUsername = strings.TrimSpace(*patch.WardriftUsername)
			}
			if patch.WardriftPassword != nil && *patch.WardriftPassword != "" {
				c.WardriftPassword = *patch.WardriftPassword
			}
			if patch.NtfyURL != nil {
				c.NtfyURL = strings.TrimSpace(*patch.NtfyURL)
			}
			if patch.CoMovingMinCells != nil && *patch.CoMovingMinCells > 0 {
				c.CoMovingMinCells = *patch.CoMovingMinCells
			}
			if patch.CoMovingMinDistM != nil && *patch.CoMovingMinDistM > 0 {
				c.CoMovingMinDistM = *patch.CoMovingMinDistM
			}
			if patch.DeauthAlertThreshold != nil && *patch.DeauthAlertThreshold > 0 {
				c.DeauthAlertThreshold = *patch.DeauthAlertThreshold
			}
			if patch.RayhunterForwardGPS != nil {
				c.RayhunterForwardGPS = *patch.RayhunterForwardGPS
			}
		})
		if s.cfgPath != "" {
			_ = s.cfg.Save(s.cfgPath)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "saved"})
	default:
		http.Error(w, "GET or PUT required", http.StatusMethodNotAllowed)
	}
}

func (s *Server) archiveExport(filename string, data []byte) {
	dir := s.cfg.Snapshot().DataDir
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, filename), data, 0o644)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
