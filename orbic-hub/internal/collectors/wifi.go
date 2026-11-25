package collectors

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"rayscout/internal/model"
)

var (
	reBSS       = regexp.MustCompile(`^BSS\s+([0-9a-fA-F:]{17})`)
	reFreq      = regexp.MustCompile(`freq:\s+(\d+)`)
	reSignal    = regexp.MustCompile(`signal:\s+(-?\d+(?:\.\d+)?)`)
	reSSID      = regexp.MustCompile(`SSID:\s*(.*)$`)
	reVendorIE  = regexp.MustCompile(`Vendor specific:\s+OUI\s+([0-9a-fA-F:]{8})`)
	reIwlistMac = regexp.MustCompile(`Address:\s+([0-9a-fA-F:]{17})`)
	reIwlistEss = regexp.MustCompile(`ESSID:"(.*)"`)
	reIwlistCh  = regexp.MustCompile(`Channel:(\d+)`)
	reIwlistFrq = regexp.MustCompile(`Frequency:(\d+\.\d+)\s*GHz`)
	reIwlistSig = regexp.MustCompile(`Signal level[=:]\s*(-?\d+)`)
)

// WiFiCollector periodically runs `iw dev <iface> scan` (with `iwlist` fallback)
// to capture 2.4 GHz and 5 GHz AP beacons and Vendor-Specific IEs on the Orbic.
type WiFiCollector struct {
	Interface string
	Interval  time.Duration
	OnObs     func(model.Observation)
}

func (w *WiFiCollector) Run(ctx context.Context) {
	if w.Interval <= 0 {
		w.Interval = 8 * time.Second
	}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	// Run one immediate scan at startup.
	w.scanOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanOnce(ctx)
		}
	}
}

func findBin(candidates ...string) string {
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return candidates[0]
}

func (w *WiFiCollector) scanOnce(ctx context.Context) {
	now := time.Now().UTC()

	// Try `iw dev <iface> scan` first (gives full RSN/WPA3 and Vendor-Specific IEs).
	iwBin := findBin("iw", "/usr/sbin/iw", "/sbin/iw")
	cmdCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	out, err := exec.CommandContext(cmdCtx, iwBin, "dev", w.Interface, "scan").Output()
	cancel()
	if err == nil && len(out) > 0 {
		for _, obs := range ParseIwScan(string(out), now) {
			if w.OnObs != nil {
				w.OnObs(obs)
			}
		}
		return
	}

	// Fallback to `iwlist <iface> scan` for older BusyBox / Qualcomm userlands.
	iwlistBin := findBin("iwlist", "/sbin/iwlist", "/usr/sbin/iwlist")
	cmdCtx2, cancel2 := context.WithTimeout(ctx, 12*time.Second)
	out2, err2 := exec.CommandContext(cmdCtx2, iwlistBin, w.Interface, "scan").Output()
	cancel2()
	if err2 == nil && len(out2) > 0 {
		for _, obs := range ParseIwlistScan(string(out2), now) {
			if w.OnObs != nil {
				w.OnObs(obs)
			}
		}
	}
}

// ParseIwScan parses Linux `iw dev <iface> scan` output into normalized observations.
func ParseIwScan(output string, now time.Time) []model.Observation {
	var results []model.Observation
	var cur *model.Observation
	var hasRSN, hasWPA, hasSAE, hasPrivacy, hasESS bool
	seenIEs := map[string]bool{}

	flush := func() {
		if cur == nil {
			return
		}
		cur.AuthMode = buildAuthMode(hasRSN, hasWPA, hasSAE, hasPrivacy, hasESS)
		if cur.Channel == 0 && cur.Frequency > 0 {
			cur.Channel = FreqToChannel(cur.Frequency)
		}
		cur.BestRSSI = cur.RSSI
		cur.SeenCount = 1
		results = append(results, *cur)
		cur = nil
		hasRSN, hasWPA, hasSAE, hasPrivacy, hasESS = false, false, false, false, false
		seenIEs = map[string]bool{}
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := reBSS.FindStringSubmatch(line); m != nil {
			flush()
			mac := strings.ToUpper(m[1])
			cur = &model.Observation{
				Type:      model.RadioWiFi,
				ID:        mac,
				OUI:       ExtractOUI(mac),
				FirstSeen: now,
				LastSeen:  now,
				Source:    "orbic_wifi",
			}
			continue
		}
		if cur == nil {
			continue
		}

		if m := reFreq.FindStringSubmatch(line); m != nil {
			if f, err := strconv.Atoi(m[1]); err == nil {
				cur.Frequency = f
				cur.Channel = FreqToChannel(f)
			}
		} else if m := reSignal.FindStringSubmatch(line); m != nil {
			if f, err := strconv.ParseFloat(m[1], 64); err == nil {
				cur.RSSI = int(f)
			}
		} else if m := reSSID.FindStringSubmatch(line); m != nil {
			cur.Name = strings.TrimSpace(m[1])
		} else if strings.HasPrefix(line, "capability:") {
			if strings.Contains(line, "ESS") {
				hasESS = true
			}
			if strings.Contains(line, "Privacy") {
				hasPrivacy = true
			}
		} else if strings.HasPrefix(line, "RSN:") {
			hasRSN = true
		} else if strings.HasPrefix(line, "WPA:") {
			hasWPA = true
		} else if strings.Contains(line, "SAE") {
			hasSAE = true
		} else if m := reVendorIE.FindStringSubmatch(line); m != nil {
			oui := strings.ToUpper(m[1])
			if !seenIEs[oui] {
				seenIEs[oui] = true
				cur.VendorIEs = append(cur.VendorIEs, oui)
			}
		}
	}
	flush()
	return results
}

// ParseIwlistScan parses legacy `iwlist <iface> scan` output as a fallback.
func ParseIwlistScan(output string, now time.Time) []model.Observation {
	var results []model.Observation
	var cur *model.Observation
	var encOn, hasWPA2, hasWPA bool

	flush := func() {
		if cur == nil {
			return
		}
		cur.AuthMode = buildAuthMode(hasWPA2, hasWPA, false, encOn, true)
		if cur.Channel == 0 && cur.Frequency > 0 {
			cur.Channel = FreqToChannel(cur.Frequency)
		}
		cur.BestRSSI = cur.RSSI
		cur.SeenCount = 1
		results = append(results, *cur)
		cur = nil
		encOn, hasWPA2, hasWPA = false, false, false
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Cell ") && strings.Contains(line, "Address:") {
			flush()
			if m := reIwlistMac.FindStringSubmatch(line); m != nil {
				mac := strings.ToUpper(m[1])
				cur = &model.Observation{
					Type:      model.RadioWiFi,
					ID:        mac,
					OUI:       ExtractOUI(mac),
					FirstSeen: now,
					LastSeen:  now,
					Source:    "orbic_wifi",
				}
			}
			continue
		}
		if cur == nil {
			continue
		}

		if m := reIwlistEss.FindStringSubmatch(line); m != nil {
			cur.Name = m[1]
		} else if m := reIwlistCh.FindStringSubmatch(line); m != nil {
			cur.Channel, _ = strconv.Atoi(m[1])
		} else if m := reIwlistFrq.FindStringSubmatch(line); m != nil {
			if ghz, err := strconv.ParseFloat(m[1], 64); err == nil {
				cur.Frequency = int(ghz * 1000)
			}
		} else if m := reIwlistSig.FindStringSubmatch(line); m != nil {
			cur.RSSI, _ = strconv.Atoi(m[1])
		} else if strings.Contains(line, "Encryption key:on") {
			encOn = true
		} else if strings.Contains(line, "WPA2") || strings.Contains(line, "802.11i") {
			hasWPA2 = true
		} else if strings.Contains(line, "WPA Version") {
			hasWPA = true
		}
	}
	flush()
	return results
}

func buildAuthMode(rsn, wpa, sae, privacy, ess bool) string {
	var b strings.Builder
	if sae {
		b.WriteString("[WPA3-SAE-CCMP]")
	}
	if rsn {
		b.WriteString("[WPA2-PSK-CCMP]")
	}
	if wpa {
		b.WriteString("[WPA-PSK-TKIP]")
	}
	if !sae && !rsn && !wpa {
		if privacy {
			b.WriteString("[WEP]")
		} else {
			b.WriteString("[OPEN]")
		}
	}
	if ess {
		b.WriteString("[ESS]")
	}
	return b.String()
}

// ExtractOUI returns the uppercase 3-octet OUI ("AA:BB:CC") from a MAC address.
func ExtractOUI(mac string) string {
	mac = strings.ToUpper(strings.TrimSpace(mac))
	if len(mac) >= 8 {
		return mac[:8]
	}
	return ""
}

// FreqToChannel converts a 2.4 GHz or 5 GHz center frequency (MHz) to an 802.11 channel number.
func FreqToChannel(freq int) int {
	switch {
	case freq == 2484:
		return 14
	case freq >= 2412 && freq <= 2472:
		return (freq - 2407) / 5
	case freq >= 5180 && freq <= 5885:
		return (freq - 5000) / 5
	default:
		return 0
	}
}
