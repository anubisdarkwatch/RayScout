package collectors

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"rayscout/internal/model"
)

var (
	// Matches +COPS: 0,0,"Verizon Wireless",7
	reCOPS = regexp.MustCompile(`\+COPS:\s*\d+,\d+,"([^"]*)"(?:,(\d+))?`)
	// Matches +CEREG: 2,1,"1A2B","01234567",7  or  +CREG: 2,1,"1A2B","0123",2
	reREG = regexp.MustCompile(`\+CE?REG:\s*\d+,\d+,"([0-9a-fA-F]+)","([0-9a-fA-F]+)"(?:,(\d+))?`)
	// Matches +CSQ: 22,99
	reCSQ = regexp.MustCompile(`\+CSQ:\s*(\d+),`)
	// Matches Quectel/Qualcomm +QENG: "servingcell","NOCONN","LTE","FDD",311,480,1A2B3C4,123,...
	reQENG = regexp.MustCompile(`\+QENG:\s*"servingcell","[^"]*","([^"]+)","[^"]*",(\d+),(\d+),([0-9a-fA-F]+),(\d+)`)
)

// Candidate AT ports on Qualcomm MDM9207 / Orbic RC400L Linux.
// Note: We NEVER touch /dev/diag so EFF Rayhunter has exclusive baseband diag access.
var defaultATPorts = []string{
	"/dev/smd7",
	"/dev/smd0",
	"/dev/ttyUSB2",
	"/dev/ttyUSB1",
	"/dev/ttyHS0",
}

// CellCollector polls serving cell tower metadata via non-exclusive AT commands.
type CellCollector struct {
	Port     string
	Interval time.Duration
	OnObs    func(model.Observation)
}

func (c *CellCollector) Run(ctx context.Context) {
	if c.Interval <= 0 {
		c.Interval = 10 * time.Second
	}
	port := c.resolvePort()
	if port == "" {
		return // Non-Orbic host or no AT port available; fail gracefully.
	}

	ticker := time.NewTicker(c.Interval)
	defer ticker.Stop()

	c.pollOnce(port)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pollOnce(port)
		}
	}
}

func (c *CellCollector) resolvePort() string {
	if c.Port != "" {
		if _, err := os.Stat(c.Port); err == nil {
			return c.Port
		}
		return ""
	}
	for _, p := range defaultATPorts {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (c *CellCollector) pollOnce(port string) {
	resp := sendATBatch(port, []string{
		"AT+CEREG=2",
		"AT+COPS?",
		"AT+CEREG?",
		"AT+CREG?",
		"AT+CSQ",
	})
	if resp == "" {
		return
	}
	if obs, ok := ParseATCellResponse(resp, time.Now().UTC()); ok && c.OnObs != nil {
		c.OnObs(obs)
	}
}

func sendATBatch(port string, cmds []string) string {
	f, err := os.OpenFile(port, os.O_RDWR, 0)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Safety watchdog: close serial fd if a non-pollable SMD device stalls.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = f.Close()
		}
	}()

	var out strings.Builder
	buf := make([]byte, 2048)

	for _, cmd := range cmds {
		_, err := f.WriteString(cmd + "\r\n")
		if err != nil {
			break
		}
		_ = f.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		n, _ := f.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
	}
	return out.String()
}

// ParseATCellResponse extracts WiGLE-compatible cellular tower info from AT command output.
func ParseATCellResponse(resp string, now time.Time) (model.Observation, bool) {
	obs := model.Observation{
		Type:      model.RadioLTE,
		AuthMode:  "LTE",
		RSSI:      -95,
		FirstSeen: now,
		LastSeen:  now,
		SeenCount: 1,
		Source:    "orbic_cell",
	}

	var operator string
	var act int = 7 // default E-UTRAN (LTE)
	var tacHex, cidHex string
	var mcc, mnc, pci int

	if m := reQENG.FindStringSubmatch(resp); m != nil {
		rat := strings.ToUpper(m[1])
		mcc, _ = strconv.Atoi(m[2])
		mnc, _ = strconv.Atoi(m[3])
		cidHex = m[4]
		pci, _ = strconv.Atoi(m[5])
		switch rat {
		case "GSM":
			obs.Type = model.RadioGSM
			obs.AuthMode = "GSM"
		case "WCDMA":
			obs.Type = model.RadioWCDMA
			obs.AuthMode = "WCDMA"
		case "NR5G-SA", "NR":
			obs.Type = model.RadioNR
			obs.AuthMode = "NR"
		default:
			obs.Type = model.RadioLTE
			obs.AuthMode = "LTE"
		}
	}

	if m := reCOPS.FindStringSubmatch(resp); m != nil {
		operator = strings.TrimSpace(m[1])
		if len(m) > 2 && m[2] != "" {
			act, _ = strconv.Atoi(m[2])
		}
	}

	if m := reREG.FindStringSubmatch(resp); m != nil {
		tacHex = m[1]
		cidHex = m[2]
		if len(m) > 3 && m[3] != "" {
			act, _ = strconv.Atoi(m[3])
		}
	}

	if cidHex == "" {
		return obs, false
	}

	tacVal, _ := strconv.ParseInt(tacHex, 16, 64)
	cidVal, _ := strconv.ParseInt(cidHex, 16, 64)

	// Map 3GPP AcT codes when QENG wasn't available.
	if mcc == 0 {
		switch act {
		case 0, 1, 3:
			obs.Type = model.RadioGSM
			obs.AuthMode = "GSM"
		case 2, 4, 5, 6:
			obs.Type = model.RadioWCDMA
			obs.AuthMode = "WCDMA"
		case 7:
			obs.Type = model.RadioLTE
			obs.AuthMode = "LTE"
		case 11, 12:
			obs.Type = model.RadioNR
			obs.AuthMode = "NR"
		}
	}

	if m := reCSQ.FindStringSubmatch(resp); m != nil {
		csq, _ := strconv.Atoi(m[1])
		if csq >= 0 && csq <= 31 {
			obs.RSSI = -113 + (csq * 2)
		}
	}
	obs.BestRSSI = obs.RSSI
	obs.CellMCC = mcc
	obs.CellMNC = mnc
	obs.CellTAC = int(tacVal)
	obs.CellCID = int(cidVal)
	obs.CellPCI = pci
	obs.Name = operator
	if obs.Name == "" {
		obs.Name = "CELL"
	}

	// WiGLE cellular ID format: MCCMNC_TAC_CID (or 00000_TAC_CID if numeric PLMN unavailable)
	if mcc > 0 {
		obs.ID = fmt.Sprintf("%d%02d_%d_%d", mcc, mnc, tacVal, cidVal)
	} else {
		obs.ID = fmt.Sprintf("CELL_%d_%d", tacVal, cidVal)
	}
	return obs, true
}
