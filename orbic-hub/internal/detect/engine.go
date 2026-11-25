package detect

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"rayscout/internal/config"
	"rayscout/internal/model"
)

// RuleFile matches rules.json format.
type RuleFile struct {
	Version     string `json:"version"`
	Description string `json:"description"`
	Rules       []Rule `json:"rules"`
}

// Rule defines a single passive RF signature rule.
type Rule struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	Severity       string   `json:"severity"`
	RadioTypes     []string `json:"radio_types"`
	OUIs           []string `json:"ouis,omitempty"`
	NameSubstrings []string `json:"name_substrings,omitempty"`
	VendorIEs      []string `json:"vendor_ies,omitempty"`
	MfgIDs         []string `json:"mfg_ids,omitempty"`
	ServiceUUIDs   []string `json:"service_uuids,omitempty"`
	RequireOpen    bool     `json:"require_open,omitempty"`
	Description    string   `json:"description,omitempty"`
}

type coMovingTrack struct {
	FirstSeen time.Time
	LastSeen  time.Time
	FirstGPS  *model.GPSFix
	Cells     map[string]struct{}
	MaxDistM  float64
	Alerted   bool
}

// Engine holds live observations, alerts, signature rules, co-moving state,
// cell-tower baseline state, and pending LoRa alert relays.
type Engine struct {
	mu            sync.RWMutex
	cfg           *config.Config
	observations  map[string]*model.Observation // key: TYPE|ID
	alerts        []model.Alert
	alertCooldown map[string]time.Time
	loraQueue     []string
	rules         RuleFile
	coMoving      map[string]*coMovingTrack
	currentCellID string
	prevCellType  model.RadioType
	prevNeighbors int
	baselineCells map[string]bool // loaded from cell_baseline.csv if present
	companion     model.CompanionStatus
	httpClient    *http.Client
	onNewObs      func(model.Observation)
}

func NewEngine(cfg *config.Config, onNewObs func(model.Observation)) *Engine {
	e := &Engine{
		cfg:           cfg,
		observations:  make(map[string]*model.Observation),
		alerts:        make([]model.Alert, 0, 128),
		alertCooldown: make(map[string]time.Time),
		loraQueue:     make([]string, 0, 32),
		coMoving:      make(map[string]*coMovingTrack),
		baselineCells: make(map[string]bool),
		httpClient:    &http.Client{Timeout: 4 * time.Second},
		onNewObs:      onNewObs,
	}
	_ = e.LoadRules(cfg.Snapshot().RulesPath)
	_ = e.LoadCellBaseline(cfg.Snapshot().CellBaselinePath)
	return e
}

// LoadRules loads signature rules from a JSON file.
func (e *Engine) LoadRules(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rf RuleFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return err
	}
	e.mu.Lock()
	e.rules = rf
	e.mu.Unlock()
	return nil
}

// SaveAndReloadRules validates, persists, and hot-reloads rules.json.
func (e *Engine) SaveAndReloadRules(path string, raw []byte) error {
	var rf RuleFile
	if err := json.Unmarshal(raw, &rf); err != nil {
		return err
	}
	if path != "" {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.rules = rf
	e.mu.Unlock()
	return nil
}

// Rules returns a copy of the active rule set.
func (e *Engine) Rules() RuleFile {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rules
}

// LoadCellBaseline loads an optional regional CSV of known cell towers (MCC,MNC,TAC,CID,...).
func (e *Engine) LoadCellBaseline(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return err
	}
	loaded := make(map[string]bool, len(rows))
	for i, row := range rows {
		if i == 0 && len(row) > 0 && strings.EqualFold(strings.TrimSpace(row[0]), "mcc") {
			continue
		}
		if len(row) >= 4 {
			key := fmt.Sprintf("%s%s_%s_%s",
				strings.TrimSpace(row[0]),
				strings.TrimSpace(row[1]),
				strings.TrimSpace(row[2]),
				strings.TrimSpace(row[3]),
			)
			loaded[key] = true
		}
	}
	e.mu.Lock()
	e.baselineCells = loaded
	e.mu.Unlock()
	return nil
}

// Ingest processes a single normalized observation through all detection pipelines.
func (e *Engine) Ingest(obs model.Observation, gps *model.GPSFix) {
	now := time.Now().UTC()
	if obs.FirstSeen.IsZero() {
		obs.FirstSeen = now
	}
	if obs.LastSeen.IsZero() {
		obs.LastSeen = now
	}
	obs.ID = strings.ToUpper(strings.TrimSpace(obs.ID))
	if obs.ID == "" {
		return
	}
	if obs.OUI == "" && len(obs.ID) >= 8 && strings.Contains(obs.ID, ":") {
		obs.OUI = obs.ID[:8]
	}
	if obs.GPS == nil && gps != nil && gps.Valid {
		cp := *gps
		obs.GPS = &cp
	}

	key := string(obs.Type) + "|" + obs.ID

	e.mu.Lock()
	// Cap memory footprint on the Orbic (512 MB RAM).
	if len(e.observations) > 15000 {
		e.evictOldestLocked(2000)
	}

	if existing, ok := e.observations[key]; ok {
		existing.LastSeen = obs.LastSeen
		existing.SeenCount++
		existing.RSSI = obs.RSSI
		if obs.RSSI > existing.BestRSSI || existing.BestRSSI == 0 {
			existing.BestRSSI = obs.RSSI
			if obs.GPS != nil && obs.GPS.Valid {
				cp := *obs.GPS
				existing.GPS = &cp
			}
		} else if existing.GPS == nil && obs.GPS != nil && obs.GPS.Valid {
			cp := *obs.GPS
			existing.GPS = &cp
		}
		if obs.Name != "" && existing.Name == "" {
			existing.Name = obs.Name
		}
		if len(obs.VendorIEs) > 0 {
			existing.VendorIEs = mergeUnique(existing.VendorIEs, obs.VendorIEs)
		}
		if len(obs.ServiceUUIDs) > 0 {
			existing.ServiceUUIDs = mergeUnique(existing.ServiceUUIDs, obs.ServiceUUIDs)
		}
		if obs.ManufacturerID != "" && existing.ManufacturerID == "" {
			existing.ManufacturerID = obs.ManufacturerID
		}
	} else {
		cp := obs
		if cp.SeenCount == 0 {
			cp.SeenCount = 1
		}
		if cp.BestRSSI == 0 {
			cp.BestRSSI = cp.RSSI
		}
		e.observations[key] = &cp
	}

	// Pipeline 1: Cellular anomaly & baseline diffing.
	if isCellular(obs.Type) {
		e.evaluateCellularLocked(obs)
	}

	// Pipeline 2: Co-moving / persistent device tracking for Wi-Fi / BLE / Probes.
	if obs.Type == model.RadioWiFi || obs.Type == model.RadioBLE || obs.Type == model.RadioWiFiProbe {
		if !e.cfg.IsWhitelisted(obs.ID) {
			e.evaluateCoMovingLocked(obs)
		}
	}

	// Pipeline 3: Modular signature matching (rules.json).
	e.evaluateSignaturesLocked(obs)
	e.mu.Unlock()

	if e.onNewObs != nil {
		e.onNewObs(obs)
	}
}

// IngestCompanionBatch handles batched telemetry from the XIAO ESP32-S3 (+ optional Wio-SX1262).
func (e *Engine) IngestCompanionBatch(batch model.CompanionIngestBatch, remoteIP string, gps *model.GPSFix) model.CompanionIngestResponse {
	now := time.Now().UTC()

	e.mu.Lock()
	e.companion.Connected = true
	e.companion.NodeID = batch.NodeID
	e.companion.HasLoRa = batch.HasLoRa
	e.companion.HasGPS = batch.HasGPS
	e.companion.LastSeen = now
	e.companion.TotalBatches++
	e.companion.TotalDeauths += int64(batch.DeauthFrames)
	e.companion.LastRemoteIP = remoteIP

	thresh := e.cfg.Snapshot().DeauthAlertThreshold
	if thresh <= 0 {
		thresh = 15
	}
	if batch.DeauthFrames >= thresh {
		e.addAlertLocked(model.Alert{
			ID:        fmt.Sprintf("deauth-%d", now.UnixNano()),
			Timestamp: now,
			Severity:  model.SeverityHigh,
			Category:  "deauth",
			Title:     "Wi-Fi Deauth/Disassoc Flood Detected",
			Detail:    fmt.Sprintf("XIAO sensor '%s' observed %d deauth/disassoc frames in one scan window.", batch.NodeID, batch.DeauthFrames),
			GPS:       gps,
		}, "deauth-flood", 45*time.Second)
	}
	e.mu.Unlock()

	for _, obs := range batch.Observations {
		e.Ingest(obs, gps)
	}

	// Piggyback pending LoRa relay alerts if this XIAO node has the Wio-SX1262 module.
	var loraOut []string
	if batch.HasLoRa {
		e.mu.Lock()
		if len(e.loraQueue) > 0 {
			maxSend := len(e.loraQueue)
			if maxSend > 3 {
				maxSend = 3
			}
			loraOut = append(loraOut, e.loraQueue[:maxSend]...)
			e.loraQueue = e.loraQueue[maxSend:]
		}
		e.mu.Unlock()
	}

	return model.CompanionIngestResponse{
		Status:     "ok",
		Accepted:   len(batch.Observations),
		LoRaAlerts: loraOut,
	}
}

// PushExternalAlert accepts alerts from external monitors like RayhunterBridge.
func (e *Engine) PushExternalAlert(a model.Alert) {
	e.mu.Lock()
	e.addAlertLocked(a, a.Category+"|"+a.Title, 30*time.Second)
	e.mu.Unlock()
}

func (e *Engine) evaluateCellularLocked(obs model.Observation) {
	now := obs.LastSeen
	prevCell := e.currentCellID
	e.currentCellID = obs.ID

	// 1) Unexpected 2G/GSM downgrade from LTE/NR.
	if obs.Type == model.RadioGSM && (e.prevCellType == model.RadioLTE || e.prevCellType == model.RadioNR) {
		e.addAlertLocked(model.Alert{
			ID:        fmt.Sprintf("cell-2g-%d", now.UnixNano()),
			Timestamp: now,
			Severity:  model.SeverityHigh,
			Category:  "cell_anomaly",
			Title:     "Cellular Downgrade to 2G/GSM",
			Detail:    fmt.Sprintf("Modem dropped from %s to GSM cell %s (%s, RSSI %d dBm).", e.prevCellType, obs.ID, obs.Name, obs.RSSI),
			DeviceID:  obs.ID,
			GPS:       obs.GPS,
		}, "cell-2g|"+obs.ID, 2*time.Minute)
	}

	// 2) Sudden collapse of neighbor cells (common around portable/temporary cells).
	if prevCell != "" && obs.ID != prevCell && e.prevNeighbors >= 3 && obs.NeighborCount == 0 && obs.RSSI > -75 {
		e.addAlertLocked(model.Alert{
			ID:        fmt.Sprintf("cell-nbr-%d", now.UnixNano()),
			Timestamp: now,
			Severity:  model.SeverityWarn,
			Category:  "cell_anomaly",
			Title:     "Strong Cell With Neighbor Drop",
			Detail:    fmt.Sprintf("Switched to %s (%d dBm) with 0 reported neighbors (down from %d).", obs.ID, obs.RSSI, e.prevNeighbors),
			DeviceID:  obs.ID,
			GPS:       obs.GPS,
		}, "cell-nbr|"+obs.ID, 5*time.Minute)
	}

	// 3) Offline regional baseline diff (when user supplies cell_baseline.csv).
	if len(e.baselineCells) > 0 && !e.baselineCells[obs.ID] && obs.RSSI > -75 {
		e.addAlertLocked(model.Alert{
			ID:        fmt.Sprintf("cell-base-%d", now.UnixNano()),
			Timestamp: now,
			Severity:  model.SeverityWarn,
			Category:  "cell_anomaly",
			Title:     "Unmapped Strong Cell Tower",
			Detail:    fmt.Sprintf("Cell %s (%s, RSSI %d dBm) is not in local regional baseline.", obs.ID, obs.Name, obs.RSSI),
			DeviceID:  obs.ID,
			GPS:       obs.GPS,
		}, "cell-base|"+obs.ID, 10*time.Minute)
	}

	e.prevCellType = obs.Type
	e.prevNeighbors = obs.NeighborCount
}

func (e *Engine) evaluateCoMovingLocked(obs model.Observation) {
	snap := e.cfg.Snapshot()
	minCells := snap.CoMovingMinCells
	if minCells <= 0 {
		minCells = 3
	}
	minDist := snap.CoMovingMinDistM
	if minDist <= 0 {
		minDist = 800
	}
	minDur := time.Duration(snap.CoMovingMinDurationS) * time.Second
	if minDur <= 0 {
		minDur = 5 * time.Minute
	}

	tr, ok := e.coMoving[obs.ID]
	if !ok {
		tr = &coMovingTrack{
			FirstSeen: obs.FirstSeen,
			LastSeen:  obs.LastSeen,
			Cells:     make(map[string]struct{}),
		}
		if obs.GPS != nil && obs.GPS.Valid {
			cp := *obs.GPS
			tr.FirstGPS = &cp
		}
		e.coMoving[obs.ID] = tr
	}
	tr.LastSeen = obs.LastSeen
	if e.currentCellID != "" {
		tr.Cells[e.currentCellID] = struct{}{}
	}
	if tr.FirstGPS != nil && obs.GPS != nil && obs.GPS.Valid {
		dist := haversineMeters(tr.FirstGPS.Latitude, tr.FirstGPS.Longitude, obs.GPS.Latitude, obs.GPS.Longitude)
		if dist > tr.MaxDistM {
			tr.MaxDistM = dist
		}
	} else if tr.FirstGPS == nil && obs.GPS != nil && obs.GPS.Valid {
		cp := *obs.GPS
		tr.FirstGPS = &cp
	}

	if tr.Alerted {
		return
	}

	byCells := len(tr.Cells) >= minCells
	byGPS := tr.MaxDistM >= minDist && tr.LastSeen.Sub(tr.FirstSeen) >= minDur

	if byCells || byGPS {
		tr.Alerted = true
		reason := fmt.Sprintf("seen across %d distinct cell towers", len(tr.Cells))
		if byGPS {
			reason = fmt.Sprintf("tracked over %.0fm across %s (%d cells)", tr.MaxDistM, tr.LastSeen.Sub(tr.FirstSeen).Round(time.Second), len(tr.Cells))
		}
		label := obs.ID
		if obs.Name != "" {
			label = fmt.Sprintf("%s (%s)", obs.Name, obs.ID)
		}
		e.addAlertLocked(model.Alert{
			ID:        fmt.Sprintf("comove-%d", obs.LastSeen.UnixNano()),
			Timestamp: obs.LastSeen,
			Severity:  model.SeverityWarn,
			Category:  "co_moving",
			Title:     "Persistent Co-Moving Device",
			Detail:    fmt.Sprintf("%s device %s %s. Whitelist if it belongs to you.", obs.Type, label, reason),
			DeviceID:  obs.ID,
			GPS:       obs.GPS,
		}, "comove|"+obs.ID, 15*time.Minute)
	}
}

func (e *Engine) evaluateSignaturesLocked(obs model.Observation) {
	for _, rule := range e.rules.Rules {
		if !matchesRule(rule, obs) {
			continue
		}
		sev := model.SeverityInfo
		switch strings.ToLower(rule.Severity) {
		case "warn", "warning":
			sev = model.SeverityWarn
		case "high", "critical":
			sev = model.SeverityHigh
		}
		cat := rule.Category
		if cat == "" {
			cat = "signature"
		}
		label := obs.ID
		if obs.Name != "" {
			label = fmt.Sprintf("%s (%s)", obs.Name, obs.ID)
		}
		e.addAlertLocked(model.Alert{
			ID:        fmt.Sprintf("sig-%s-%d", rule.ID, obs.LastSeen.UnixNano()),
			Timestamp: obs.LastSeen,
			Severity:  sev,
			Category:  cat,
			Title:     rule.Name,
			Detail:    fmt.Sprintf("%s matched %s [RSSI %d dBm]. %s", obs.Type, label, obs.RSSI, rule.Description),
			DeviceID:  obs.ID,
			GPS:       obs.GPS,
		}, "sig|"+rule.ID+"|"+obs.ID, 5*time.Minute)
	}
}

func matchesRule(rule Rule, obs model.Observation) bool {
	if len(rule.RadioTypes) > 0 && !containsFold(rule.RadioTypes, string(obs.Type)) {
		return false
	}
	if rule.RequireOpen && !strings.Contains(strings.ToUpper(obs.AuthMode), "OPEN") {
		return false
	}

	hasCondition := false
	matched := false

	if len(rule.VendorIEs) > 0 {
		hasCondition = true
		for _, want := range rule.VendorIEs {
			if containsFold(obs.VendorIEs, want) {
				matched = true
				break
			}
		}
	}
	if len(rule.ServiceUUIDs) > 0 {
		hasCondition = true
		for _, want := range rule.ServiceUUIDs {
			for _, got := range obs.ServiceUUIDs {
				if strings.Contains(strings.ToUpper(got), strings.ToUpper(want)) {
					matched = true
					break
				}
			}
		}
	}
	if len(rule.OUIs) > 0 {
		hasCondition = true
		if obs.OUI != "" && containsFold(rule.OUIs, obs.OUI) {
			matched = true
		}
	}
	if len(rule.MfgIDs) > 0 {
		hasCondition = true
		if obs.ManufacturerID != "" && containsFold(rule.MfgIDs, obs.ManufacturerID) {
			matched = true
		}
	}
	if len(rule.NameSubstrings) > 0 {
		hasCondition = true
		lowerName := strings.ToLower(obs.Name)
		for _, sub := range rule.NameSubstrings {
			if sub != "" && strings.Contains(lowerName, strings.ToLower(sub)) {
				matched = true
				break
			}
		}
	}

	return hasCondition && matched
}

func (e *Engine) addAlertLocked(a model.Alert, dedupKey string, cooldown time.Duration) {
	now := time.Now().UTC()
	if prev, ok := e.alertCooldown[dedupKey]; ok && now.Sub(prev) < cooldown {
		return
	}
	e.alertCooldown[dedupKey] = now

	// Queue high/warn alerts for Wio-SX1262 LoRa relay.
	if a.Severity == model.SeverityHigh || a.Severity == model.SeverityWarn {
		msg := fmt.Sprintf("[RAYSCOUT %s] %s: %s", strings.ToUpper(string(a.Severity)), a.Title, a.DeviceID)
		if len(msg) > 180 {
			msg = msg[:180]
		}
		if len(e.loraQueue) < 32 {
			e.loraQueue = append(e.loraQueue, msg)
			a.RelayedLoRa = e.companion.HasLoRa && time.Since(e.companion.LastSeen) < 60*time.Second
		}
	}

	e.alerts = append([]model.Alert{a}, e.alerts...)
	if len(e.alerts) > 250 {
		e.alerts = e.alerts[:250]
	}

	ntfyURL := e.cfg.Snapshot().NtfyURL
	if ntfyURL != "" && (a.Severity == model.SeverityHigh || a.Severity == model.SeverityWarn) {
		go e.sendNtfy(ntfyURL, a)
	}
}

func (e *Engine) sendNtfy(url string, a model.Alert) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(a.Detail))
	if err != nil {
		return
	}
	req.Header.Set("Title", fmt.Sprintf("RayScout [%s]: %s", strings.ToUpper(string(a.Severity)), a.Title))
	if a.Severity == model.SeverityHigh {
		req.Header.Set("Priority", "urgent")
	}
	resp, err := e.httpClient.Do(req)
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}

// Observations returns a sorted slice of observations (most recently seen first).
func (e *Engine) Observations(filterType string, limit int) []model.Observation {
	e.mu.RLock()
	out := make([]model.Observation, 0, len(e.observations))
	for _, o := range e.observations {
		if filterType != "" && !strings.EqualFold(string(o.Type), filterType) {
			continue
		}
		out = append(out, *o)
	}
	e.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Alerts returns recent alerts (newest first).
func (e *Engine) Alerts() []model.Alert {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cp := make([]model.Alert, len(e.alerts))
	copy(cp, e.alerts)
	return cp
}

// AckAlert marks an alert as acknowledged.
func (e *Engine) AckAlert(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.alerts {
		if e.alerts[i].ID == id {
			e.alerts[i].Acked = true
			return true
		}
	}
	return false
}

// CompanionStatus returns the current status of the optional XIAO ESP32-S3 node.
func (e *Engine) CompanionStatus() model.CompanionStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := e.companion
	if st.Connected && time.Since(st.LastSeen) > 45*time.Second {
		st.Connected = false
	}
	return st
}

// Stats returns counts grouped by radio type and alert severity.
func (e *Engine) Stats() map[string]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	stats := map[string]int{
		"total_obs":    len(e.observations),
		"wifi":         0,
		"ble":          0,
		"cell":         0,
		"probe":        0,
		"lora":         0,
		"gps_tagged":   0,
		"alerts_total": len(e.alerts),
		"alerts_open":  0,
	}
	for _, o := range e.observations {
		switch o.Type {
		case model.RadioWiFi:
			stats["wifi"]++
		case model.RadioBLE:
			stats["ble"]++
		case model.RadioWiFiProbe:
			stats["probe"]++
		case model.RadioLoRa:
			stats["lora"]++
		default:
			if isCellular(o.Type) {
				stats["cell"]++
			}
		}
		if o.GPS != nil && o.GPS.Valid {
			stats["gps_tagged"]++
		}
	}
	for _, a := range e.alerts {
		if !a.Acked {
			stats["alerts_open"]++
		}
	}
	return stats
}

func (e *Engine) evictOldestLocked(n int) {
	type kv struct {
		k string
		t time.Time
	}
	items := make([]kv, 0, len(e.observations))
	for k, v := range e.observations {
		items = append(items, kv{k: k, t: v.LastSeen})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].t.Before(items[j].t) })
	for i := 0; i < n && i < len(items); i++ {
		delete(e.observations, items[i].k)
	}
}

func isCellular(rt model.RadioType) bool {
	return rt == model.RadioLTE || rt == model.RadioGSM || rt == model.RadioWCDMA || rt == model.RadioNR
}

func containsFold(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}

func mergeUnique(dst, src []string) []string {
	seen := make(map[string]bool, len(dst))
	for _, d := range dst {
		seen[strings.ToUpper(d)] = true
	}
	for _, s := range src {
		u := strings.ToUpper(s)
		if !seen[u] {
			seen[u] = true
			dst = append(dst, s)
		}
	}
	return dst
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthR = 6371000.0
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthR * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
