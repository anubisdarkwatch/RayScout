package main

import (
	"bufio"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed payload/*
var payloadFS embed.FS

func main() {
	reader := bufio.NewReader(os.Stdin)

	printBanner()

	adbPath, err := findADB()
	if err != nil {
		fmt.Println("[!] ADB (Android Debug Bridge) was not found on this PC.")
		fmt.Println("    Fix: Run 'scoop install adb' or 'winget install Google.PlatformTools',")
		fmt.Println("    then double-click RayScout-Installer.exe again.")
		waitEnter(reader)
		os.Exit(1)
	}
	fmt.Printf("[OK] Found ADB: %s\n", adbPath)

	for {
		fmt.Println()
		serial, ok := detectOrbic(adbPath)
		if ok {
			fmt.Printf("[OK] Orbic RC400L detected via USB (%s)\n", serial)
			rhRunning := checkRemoteRunning(adbPath, "rayhunter")
			rsRunning := checkRemoteRunning(adbPath, "rayscout")
			rootMode := detectRootMode(adbPath)
			fmt.Printf("     EFF Rayhunter : %s\n", statusLabel(rhRunning))
			fmt.Printf("     RayScout Hub  : %s\n", statusLabel(rsRunning))
			fmt.Printf("     Root Access   : %s\n", rootMode)
		} else {
			fmt.Println("[..] No Orbic device detected over USB ADB yet.")
			fmt.Println("     -> Power on your Orbic (with Rayhunter installed) and plug it in")
			fmt.Println("        using a data-capable USB-C cable, then press [R] to rescan.")
		}

		fmt.Println()
		fmt.Println("Choose an action:")
		fmt.Println("  [1] Install / Update RayScout on Orbic (Auto-starts on boot - Recommended)")
		fmt.Println("  [2] Temporary Test Run (Runs until Orbic reboots, no system changes)")
		fmt.Println("  [3] Open Dashboards Over USB (No Wi-Fi switch needed -> localhost:8090 & :8080)")
		fmt.Println("  [4] View Live RayScout Logs (Troubleshooting)")
		fmt.Println("  [5] Uninstall RayScout (Leaves EFF Rayhunter untouched)")
		fmt.Println("  [R] Rescan USB Connection")
		fmt.Println("  [Q] Quit")
		fmt.Print("\nSelection > ")

		line, _ := reader.ReadString('\n')
		choice := strings.ToUpper(strings.TrimSpace(line))

		switch choice {
		case "1":
			if !ensureDevice(adbPath) {
				continue
			}
			if err := installRayScout(adbPath, true); err != nil {
				fmt.Printf("\n[ERROR] Install failed: %v\n", err)
			} else {
				fmt.Println("\n[SUCCESS] RayScout installed and running on your Orbic!")
				setupUSBForwardAndOpen(adbPath)
			}
		case "2":
			if !ensureDevice(adbPath) {
				continue
			}
			if err := installRayScout(adbPath, false); err != nil {
				fmt.Printf("\n[ERROR] Test run failed: %v\n", err)
			} else {
				fmt.Println("\n[SUCCESS] RayScout is running in temporary mode!")
				setupUSBForwardAndOpen(adbPath)
			}
		case "3":
			if !ensureDevice(adbPath) {
				continue
			}
			setupUSBForwardAndOpen(adbPath)
		case "4":
			if !ensureDevice(adbPath) {
				continue
			}
			fmt.Println("\n--- Last 40 lines of /data/rayscout/rayscout.log ---")
			out, err := runADB(adbPath, "shell", "tail -n 40 /data/rayscout/rayscout.log 2>/dev/null || echo 'No log file yet.'")
			if err != nil {
				fmt.Printf("[ERROR] %v\n", err)
			} else {
				fmt.Println(out)
			}
		case "5":
			if !ensureDevice(adbPath) {
				continue
			}
			if err := uninstallRayScout(adbPath); err != nil {
				fmt.Printf("\n[ERROR] Uninstall failed: %v\n", err)
			} else {
				fmt.Println("\n[SUCCESS] RayScout removed. EFF Rayhunter was left untouched.")
			}
		case "R", "":
			continue
		case "Q":
			fmt.Println("Bye!")
			return
		default:
			fmt.Println("Unknown option. Enter 1-5, R, or Q.")
		}
	}
}

func printBanner() {
	fmt.Println("===============================================================")
	fmt.Println("   RAYSCOUT ONE-CLICK INSTALLER // Orbic RC400L + Rayhunter    ")
	fmt.Println("===============================================================")
	fmt.Println("Zero Linux commands needed. Everything is bundled in this tool.")
	fmt.Println()
}

func statusLabel(running bool) string {
	if running {
		return "RUNNING"
	}
	return "Not running"
}

func findADB() (string, error) {
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	localApp := os.Getenv("LOCALAPPDATA")
	exeDir, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exeDir), "adb.exe"),
		filepath.Join(home, "scoop", "shims", "adb.exe"),
		filepath.Join(localApp, "Android", "Sdk", "platform-tools", "adb.exe"),
		`C:\platform-tools\adb.exe`,
	}
	for _, c := range candidates {
		if c != "" {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("adb not found")
}

func runADB(adbPath string, args ...string) (string, error) {
	cmd := exec.Command(adbPath, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func detectOrbic(adbPath string) (string, bool) {
	out, err := runADB(adbPath, "devices")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			return fields[0], true
		}
	}
	return "", false
}

func ensureDevice(adbPath string) bool {
	if _, ok := detectOrbic(adbPath); !ok {
		fmt.Println("\n[!] Please connect your powered-on Orbic RC400L via USB first.")
		return false
	}
	return true
}

func checkRemoteRunning(adbPath, name string) bool {
	out, err := runADB(adbPath, "shell", fmt.Sprintf("ps | grep %s | grep -v grep", name))
	return err == nil && strings.Contains(out, name)
}

// detectRootMode checks working root escalation methods in priority order:
// "direct" (adb shell is uid=0), "rootshell" (working /bin/rootshell),
// "nc24" (DagShell root shell on 127.0.0.1:24), or "none".
func detectRootMode(adbPath string) string {
	if out, err := runADB(adbPath, "shell", "id -u"); err == nil && strings.TrimSpace(out) == "0" {
		return "direct"
	}
	if out, err := runADB(adbPath, "shell", "/bin/rootshell -c 'id -u' 2>/dev/null"); err == nil && strings.TrimSpace(out) == "0" {
		return "rootshell"
	}
	if out, err := runADB(adbPath, "shell", "printf 'id -u\\nexit\\n' | busybox nc 127.0.0.1 24 2>/dev/null"); err == nil && strings.TrimSpace(out) == "0" {
		return "nc24"
	}
	return "none"
}

// runRootShell executes a shell command as root and returns real exit status.
func runRootShell(adbPath, rootMode, cmd string) (string, error) {
	switch rootMode {
	case "direct":
		return runADB(adbPath, "shell", cmd)
	case "rootshell":
		escaped := strings.ReplaceAll(cmd, `'`, `'\''`)
		return runADB(adbPath, "shell", fmt.Sprintf("/bin/rootshell -c '%s'", escaped))
	case "nc24":
		escaped := strings.ReplaceAll(cmd, `'`, `'\''`)
		wrapped := fmt.Sprintf("printf '%%s\\necho __RC__$?\\nexit\\n' '%s' | busybox nc 127.0.0.1 24", escaped)
		out, err := runADB(adbPath, "shell", wrapped)
		if err != nil {
			return out, err
		}
		lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
		var clean []string
		rc := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "__RC__") {
				if strings.TrimSpace(strings.TrimPrefix(l, "__RC__")) != "0" {
					rc = 1
				}
				continue
			}
			clean = append(clean, l)
		}
		joined := strings.TrimSpace(strings.Join(clean, "\n"))
		if rc != 0 {
			return joined, fmt.Errorf("remote command failed (rc=%d): %s", rc, joined)
		}
		return joined, nil
	default:
		return "", fmt.Errorf("no root access method available")
	}
}

func pushAsRoot(adbPath, rootMode, localPath, remotePath string) error {
	if rootMode == "direct" {
		if out, err := runADB(adbPath, "push", localPath, remotePath); err != nil {
			return fmt.Errorf("%v (%s)", err, out)
		}
		return nil
	}
	tmpRemote := "/tmp/rayscout_stage_" + filepath.Base(remotePath)
	if out, err := runADB(adbPath, "push", localPath, tmpRemote); err != nil {
		return fmt.Errorf("stage push failed: %v (%s)", err, out)
	}
	if out, err := runRootShell(adbPath, rootMode, fmt.Sprintf("mv -f '%s' '%s'", tmpRemote, remotePath)); err != nil {
		return fmt.Errorf("root move failed: %v (%s)", err, out)
	}
	return nil
}

func extractPayload() (string, error) {
	tmpDir, err := os.MkdirTemp("", "rayscout-payload-*")
	if err != nil {
		return "", err
	}
	entries, err := payloadFS.ReadDir("payload")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := payloadFS.ReadFile("payload/" + e.Name())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(tmpDir, e.Name()), data, 0o644); err != nil {
			return "", err
		}
	}
	return tmpDir, nil
}

func installRayScout(adbPath string, persistent bool) error {
	rootMode := detectRootMode(adbPath)
	if rootMode == "none" {
		return fmt.Errorf("ADB is connected, but neither uid=0 nor Rayhunter's /bin/rootshell was found. Run EFF's Rayhunter installer first to enable root access")
	}

	tmpDir, err := extractPayload()
	if err != nil {
		return fmt.Errorf("extracting bundled files: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("\n[1/5] Preparing /data/rayscout on Orbic (root mode: %s)...\n", rootMode)
	if _, err := runRootShell(adbPath, rootMode, "mkdir -p /data/rayscout/data"); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	fmt.Println("[2/5] Stopping any previous RayScout process...")
	_, _ = runRootShell(adbPath, rootMode, "/etc/init.d/rayscout_daemon stop 2>/dev/null || killall rayscout 2>/dev/null || true")

	fmt.Println("[3/5] Uploading RayScout ARMv7 binary and signature rules...")
	if err := pushAsRoot(adbPath, rootMode, filepath.Join(tmpDir, "rayscout-linux-armv7"), "/data/rayscout/rayscout"); err != nil {
		return fmt.Errorf("push binary failed: %w", err)
	}
	if err := pushAsRoot(adbPath, rootMode, filepath.Join(tmpDir, "rules.json"), "/data/rayscout/rules.json"); err != nil {
		return fmt.Errorf("push rules failed: %w", err)
	}

	// Preserve existing user config.json (WiGLE/WDGWars tokens!) on upgrades.
	if _, err := runRootShell(adbPath, rootMode, "test -f /data/rayscout/config.json"); err != nil {
		if err := pushAsRoot(adbPath, rootMode, filepath.Join(tmpDir, "config.example.json"), "/data/rayscout/config.json"); err != nil {
			return fmt.Errorf("push default config failed: %w", err)
		}
	} else {
		fmt.Println("      Existing config.json detected — keeping your saved tokens & settings.")
	}

	if _, err := runRootShell(adbPath, rootMode, "chmod +x /data/rayscout/rayscout"); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	if persistent {
		fmt.Println("[4/5] Installing auto-start boot service (preserving Rayhunter)...")
		if out, err := runRootShell(adbPath, rootMode, "mount -o remount,rw /"); err != nil {
			return fmt.Errorf("remount rw failed: %v (%s)", err, out)
		}
		if err := pushAsRoot(adbPath, rootMode, filepath.Join(tmpDir, "rayscout_daemon"), "/etc/init.d/rayscout_daemon"); err != nil {
			_, _ = runRootShell(adbPath, rootMode, "mount -o remount,ro /")
			return fmt.Errorf("push init script failed: %w", err)
		}
		_, err := runRootShell(adbPath, rootMode, "chmod 755 /etc/init.d/rayscout_daemon && ln -sf /etc/init.d/rayscout_daemon /etc/rc5.d/S99rayscout")
		_, _ = runRootShell(adbPath, rootMode, "mount -o remount,ro /")
		if err != nil {
			return fmt.Errorf("enable boot symlink failed: %w", err)
		}

		fmt.Println("[5/5] Starting RayScout service...")
		if out, err := runRootShell(adbPath, rootMode, "/etc/init.d/rayscout_daemon start"); err != nil {
			return fmt.Errorf("start failed: %v (%s)", err, out)
		}
	} else {
		fmt.Println("[4/5] Skipping persistent boot script (temporary test mode)...")
		fmt.Println("[5/5] Opening firewall ports and starting RayScout in background...")
		_, _ = runRootShell(adbPath, rootMode, "iptables -C INPUT -p tcp --dport 8090 -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport 8090 -j ACCEPT; iptables -C INPUT -p tcp --dport 10110 -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport 10110 -j ACCEPT; iptables -C INPUT -p udp --dport 10110 -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport 10110 -j ACCEPT")
		_, _ = runRootShell(adbPath, rootMode, "start-stop-daemon -S -b --make-pidfile --pidfile /tmp/rayscout.pid --startas /bin/sh -- -c 'export PATH=/usr/sbin:/usr/bin:/sbin:/bin; exec /data/rayscout/rayscout -config /data/rayscout/config.json >> /data/rayscout/rayscout.log 2>&1'")
	}

	time.Sleep(1500 * time.Millisecond)
	if !checkRemoteRunning(adbPath, "rayscout") {
		logs, _ := runRootShell(adbPath, rootMode, "tail -n 20 /data/rayscout/rayscout.log 2>/dev/null")
		return fmt.Errorf("process did not stay running. Logs:\n%s", logs)
	}
	return nil
}

func uninstallRayScout(adbPath string) error {
	rootMode := detectRootMode(adbPath)
	fmt.Println("\n[1/3] Stopping RayScout...")
	_, _ = runRootShell(adbPath, rootMode, "/etc/init.d/rayscout_daemon stop 2>/dev/null || killall rayscout 2>/dev/null || true")

	fmt.Println("[2/3] Removing boot service...")
	_, _ = runRootShell(adbPath, rootMode, "mount -o remount,rw / && rm -f /etc/rc5.d/S99rayscout /etc/init.d/rayscout_daemon && mount -o remount,ro /")

	fmt.Println("[3/3] Removing /data/rayscout...")
	_, err := runRootShell(adbPath, rootMode, "rm -rf /data/rayscout")
	return err
}

func setupUSBForwardAndOpen(adbPath string) {
	fmt.Println("\nSetting up USB port forwarding (no Wi-Fi switching required)...")
	_, _ = runADB(adbPath, "forward", "tcp:8090", "tcp:8090")
	_, _ = runADB(adbPath, "forward", "tcp:8080", "tcp:8080")
	fmt.Println("  -> RayScout Dashboard : http://localhost:8090  (or http://192.168.1.1:8090 on Orbic Wi-Fi)")
	fmt.Println("  -> EFF Rayhunter      : http://localhost:8080  (or http://192.168.1.1:8080 on Orbic Wi-Fi)")
	openBrowser("http://localhost:8090")
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = exec.Command("xdg-open", url).Start()
	}
}

func waitEnter(r *bufio.Reader) {
	fmt.Print("\nPress Enter to exit...")
	_, _ = r.ReadString('\n')
}
