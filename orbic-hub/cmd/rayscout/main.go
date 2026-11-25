package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rayscout/internal/collectors"
	"rayscout/internal/config"
	"rayscout/internal/detect"
	"rayscout/internal/model"
	"rayscout/internal/server"
)

const version = "0.1.0"

func main() {
	cfgPath := flag.String("config", "/data/rayscout/config.json", "Path to config.json")
	rulesOverride := flag.String("rules", "", "Optional override path to rules.json")
	listenOverride := flag.String("listen", "", "Optional override HTTP listen address (e.g. :8090)")
	demoMode := flag.Bool("demo", false, "Run with simulated Wi-Fi/LTE/BLE/GPS data for desktop testing")
	showVer := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Printf("rayscout %s\n", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config %s: %v", *cfgPath, err)
	}
	if *rulesOverride != "" {
		cfg.Update(func(c *config.Config) { c.RulesPath = *rulesOverride })
	}
	if *listenOverride != "" {
		cfg.Update(func(c *config.Config) { c.ListenAddr = *listenOverride })
	}
	if *demoMode {
		cfg.Update(func(c *config.Config) {
			if c.DataDir == "/data/rayscout/data" {
				c.DataDir = "./demo-data"
			}
		})
	}

	snap := cfg.Snapshot()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	gpsMgr := collectors.NewGPSManager(snap.RayhunterURL, snap.RayhunterForwardGPS)
	engine := detect.NewEngine(cfg, nil)
	rhBridge := collectors.NewRayhunterBridge(snap.RayhunterURL, func(a model.Alert) {
		a.GPS = gpsMgr.Current()
		engine.PushExternalAlert(a)
	})

	// Start NMEA TCP/UDP GPS listener (works with ShareGPS / GPS2IP / gpsd).
	gpsMgr.RunNMEAListener(ctx, snap.NMEAListenAddr)

	if *demoMode {
		log.Printf("rayscout %s starting in DEMO mode on %s", version, snap.ListenAddr)
		demo := &collectors.DemoCollector{
			GPS: gpsMgr,
			OnObs: func(obs model.Observation) {
				engine.Ingest(obs, gpsMgr.Current())
			},
		}
		go demo.Run(ctx)
	} else {
		log.Printf("rayscout %s starting on %s (iface=%s, rayhunter=%s)", version, snap.ListenAddr, snap.WiFiInterface, snap.RayhunterURL)
		wifiCol := &collectors.WiFiCollector{
			Interface: snap.WiFiInterface,
			Interval:  time.Duration(snap.WiFiScanIntervalS) * time.Second,
			OnObs: func(obs model.Observation) {
				engine.Ingest(obs, gpsMgr.Current())
			},
		}
		cellCol := &collectors.CellCollector{
			Port:     snap.CellATPort,
			Interval: time.Duration(snap.CellPollIntervalS) * time.Second,
			OnObs: func(obs model.Observation) {
				engine.Ingest(obs, gpsMgr.Current())
			},
		}
		go wifiCol.Run(ctx)
		go cellCol.Run(ctx)
		go rhBridge.Run(ctx)
	}

	srv := server.New(cfg, *cfgPath, engine, gpsMgr, rhBridge, *demoMode)
	httpSrv := &http.Server{
		Addr:              snap.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	displayPort := snap.ListenAddr
	if idx := len(displayPort) - 1; idx >= 0 {
		for i := len(displayPort) - 1; i >= 0; i-- {
			if displayPort[i] == ':' {
				displayPort = displayPort[i:]
				break
			}
		}
	}
	log.Printf("Web dashboard ready at http://localhost%s (Orbic AP: http://192.168.1.1%s)", displayPort, displayPort)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server error: %v", err)
	}
}
