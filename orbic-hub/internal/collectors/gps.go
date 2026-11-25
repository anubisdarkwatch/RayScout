package collectors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rayscout/internal/model"
)

// GPSManager aggregates fixes from multiple sources (XIAO hardware GPS, NMEA TCP/UDP,
// and Phone HTTP Bridge) and optionally forwards fixes to local EFF Rayhunter.
type GPSManager struct {
	mu             sync.RWMutex
	latest         *model.GPSFix
	maxAge         time.Duration
	rayhunterURL   string
	forwardToRH    bool
	lastForwarded  time.Time
	httpClient     *http.Client
}

func NewGPSManager(rayhunterURL string, forwardToRH bool) *GPSManager {
	return &GPSManager{
		maxAge:       20 * time.Second,
		rayhunterURL: strings.TrimRight(rayhunterURL, "/"),
		forwardToRH:  forwardToRH,
		httpClient:   &http.Client{Timeout: 2 * time.Second},
	}
}

// Update stores a new GPS fix if valid, respecting source priority (xiao_gps > others).
func (g *GPSManager) Update(fix model.GPSFix) {
	if !fix.Valid || (fix.Latitude == 0 && fix.Longitude == 0) {
		return
	}
	if fix.Timestamp.IsZero() {
		fix.Timestamp = time.Now().UTC()
	}

	g.mu.Lock()
	// Prefer hardware XIAO GPS over phone bridge if XIAO fix is fresh (<10s).
	if g.latest != nil && g.latest.Source == "xiao_gps" && fix.Source != "xiao_gps" {
		if time.Since(g.latest.Timestamp) < 10*time.Second {
			g.mu.Unlock()
			return
		}
	}
	cp := fix
	g.latest = &cp
	shouldForward := g.forwardToRH && g.rayhunterURL != "" && time.Since(g.lastForwarded) >= 5*time.Second
	if shouldForward {
		g.lastForwarded = time.Now()
	}
	g.mu.Unlock()

	if shouldForward {
		go g.forwardToRayhunter(cp)
	}
}

// Current returns a copy of the latest fresh GPS fix, or nil if stale/missing.
func (g *GPSManager) Current() *model.GPSFix {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.latest == nil || time.Since(g.latest.Timestamp) > g.maxAge {
		return nil
	}
	cp := *g.latest
	return &cp
}

func (g *GPSManager) forwardToRayhunter(fix model.GPSFix) {
	// EFF Rayhunter's GPS endpoint accepts JSON latitude/longitude updates.
	payload := map[string]any{
		"latitude":  fix.Latitude,
		"longitude": fix.Longitude,
		"altitude":  fix.Altitude,
		"accuracy":  fix.Accuracy,
		"timestamp": fix.Timestamp.Unix(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, g.rayhunterURL+"/api/gps", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}

// RunNMEAListener starts both TCP and UDP NMEA-0183 listeners (compatible with ShareGPS, GPS2IP, gpsd).
func (g *GPSManager) RunNMEAListener(ctx context.Context, listenAddr string) {
	if listenAddr == "" {
		return
	}
	go g.runNMEATCP(ctx, listenAddr)
	go g.runNMEAUDP(ctx, listenAddr)
}

func (g *GPSManager) runNMEATCP(ctx context.Context, addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				if fix, ok := ParseNMEASentence(sc.Text(), "nmea_tcp"); ok {
					g.Update(fix)
				}
			}
		}(conn)
	}
}

func (g *GPSManager) runNMEAUDP(ctx context.Context, addr string) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return
	}
	go func() {
		<-ctx.Done()
		pc.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			if fix, ok := ParseNMEASentence(line, "nmea_udp"); ok {
				g.Update(fix)
			}
		}
	}
}

// ParseNMEASentence parses $GPGGA/$GNGGA and $GPRMC/$GNRMC sentences into a GPSFix.
func ParseNMEASentence(line, source string) (model.GPSFix, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "$") {
		return model.GPSFix{}, false
	}
	if idx := strings.Index(line, "*"); idx >= 0 {
		line = line[:idx]
	}
	parts := strings.Split(line, ",")
	if len(parts) < 7 {
		return model.GPSFix{}, false
	}

	head := parts[0]
	switch {
	case strings.HasSuffix(head, "GGA") && len(parts) >= 10:
		if parts[6] == "" || parts[6] == "0" {
			return model.GPSFix{}, false // No fix
		}
		lat, ok1 := parseNMEACoord(parts[2], parts[3])
		lon, ok2 := parseNMEACoord(parts[4], parts[5])
		if !ok1 || !ok2 {
			return model.GPSFix{}, false
		}
		alt, _ := strconv.ParseFloat(parts[9], 64)
		hdop, _ := strconv.ParseFloat(parts[8], 64)
		acc := 5.0
		if hdop > 0 {
			acc = math.Round(hdop*5.0*10) / 10
		}
		return model.GPSFix{
			Latitude:  lat,
			Longitude: lon,
			Altitude:  alt,
			Accuracy:  acc,
			Source:    source,
			Timestamp: time.Now().UTC(),
			Valid:     true,
		}, true

	case strings.HasSuffix(head, "RMC") && len(parts) >= 8:
		if parts[2] != "A" {
			return model.GPSFix{}, false // Void fix
		}
		lat, ok1 := parseNMEACoord(parts[3], parts[4])
		lon, ok2 := parseNMEACoord(parts[5], parts[6])
		if !ok1 || !ok2 {
			return model.GPSFix{}, false
		}
		knots, _ := strconv.ParseFloat(parts[7], 64)
		return model.GPSFix{
			Latitude:  lat,
			Longitude: lon,
			Accuracy:  8.0,
			Speed:     knots * 0.514444, // m/s
			Source:    source,
			Timestamp: time.Now().UTC(),
			Valid:     true,
		}, true
	}

	return model.GPSFix{}, false
}

func parseNMEACoord(val, hemi string) (float64, bool) {
	if val == "" || hemi == "" {
		return 0, false
	}
	dot := strings.Index(val, ".")
	if dot < 3 {
		return 0, false
	}
	degStr := val[:dot-2]
	minStr := val[dot-2:]
	deg, err1 := strconv.ParseFloat(degStr, 64)
	min, err2 := strconv.ParseFloat(minStr, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	coord := deg + (min / 60.0)
	if hemi == "S" || hemi == "W" {
		coord = -coord
	}
	return coord, true
}
