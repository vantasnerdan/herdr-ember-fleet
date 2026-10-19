// Package service installs ember-fleet roles as per-user services: systemd
// user units (with lingering, so they run without a login) on Linux and
// launchd agents on macOS. A public hub that must bind :443 gets a system
// unit that runs as the same (non-root) user with only CAP_NET_BIND_SERVICE.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

// Executable is the absolute, resolved path of this binary.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func unitName(role string) string { return "herdr-ember-fleet-" + role }

var systemdUnit = template.Must(template.New("u").Parse(`[Unit]
Description=Herdr Ember Fleet {{.Role}}
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
ExecStart={{.Exec}}
Restart=always
RestartSec=2
{{- if .KeepChildren}}
# Keep a herdr server the dock started running when the dock restarts.
KillMode=process
{{- end}}
{{- if .System}}
User={{.User}}
Environment=HOME={{.Home}}
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
{{- end}}

[Install]
WantedBy={{if .System}}multi-user.target{{else}}default.target{{end}}
`))

var launchdPlist = template.Must(template.New("p").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>{{range .Args}}
    <string>{{.}}</string>{{end}}
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>{{.Log}}</string>
  <key>StandardErrorPath</key><string>{{.Log}}</string>
</dict>
</plist>
`))

// Install writes and starts the service for role ("relay", "hub" or "dock"),
// running `<this binary> <role> serve <args...>`.
func Install(role string, args []string, system bool) (string, error) {
	exe, err := Executable()
	if err != nil {
		return "", err
	}
	full := append([]string{exe, role, "serve"}, args...)
	switch runtime.GOOS {
	case "linux":
		return installSystemd(role, full, system)
	case "darwin":
		if system {
			return "", errors.New("--system is for Linux; on macOS the hub listens on a high port behind your own proxy")
		}
		return installLaunchd(role, full)
	}
	return "", fmt.Errorf("services are not supported on %s; run `ember-fleet %s serve` yourself", runtime.GOOS, role)
}

func quote(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if strings.ContainsAny(a, " \t\"'\\$%") {
			b.WriteString(`"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`).Replace(a) + `"`)
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}

func installSystemd(role string, full []string, system bool) (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	launchdData := map[string]any{
		"Role": role, "Exec": quote(full), "KeepChildren": role == "dock", "System": system,
		"User": u.Username, "Home": u.HomeDir,
	}
	if err := systemdUnit.Execute(&buf, launchdData); err != nil {
		return "", err
	}
	name := unitName(role) + ".service"
	if system {
		path := "/etc/systemd/system/" + name
		tee := exec.Command("sudo", "tee", path)
		tee.Stdin = &buf
		tee.Stdout = nil
		tee.Stderr = os.Stderr
		if err := tee.Run(); err != nil {
			return "", fmt.Errorf("writing %s needs sudo: %w", path, err)
		}
		if err := run("sudo", "systemctl", "daemon-reload"); err != nil {
			return "", err
		}
		if err := run("sudo", "systemctl", "enable", "--now", name); err != nil {
			return "", err
		}
		run("sudo", "systemctl", "restart", name)
		return fmt.Sprintf("installed system service %s (runs as %s); logs: journalctl -u %s -f", name, u.Username, unitName(role)), nil
	}
	// `sudo -iu agent` shells have no XDG_RUNTIME_DIR; systemctl --user needs it.
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		if rd := fmt.Sprintf("/run/user/%d", os.Getuid()); dirExists(rd) {
			os.Setenv("XDG_RUNTIME_DIR", rd)
		}
	}
	lingerMsg := ""
	if out, _ := exec.Command("loginctl", "show-user", u.Username, "-p", "Linger", "--value").Output(); strings.TrimSpace(string(out)) != "yes" {
		if err := exec.Command("loginctl", "enable-linger", u.Username).Run(); err != nil {
			lingerMsg = "\nTo keep it running when you are logged out, an administrator runs: sudo loginctl enable-linger " + u.Username
		} else {
			lingerMsg = "\nturned on lingering, so it keeps running when you are logged out"
		}
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" || !dirExists(os.Getenv("XDG_RUNTIME_DIR")) {
		return "", fmt.Errorf("no user service manager is running for %s; an administrator runs `sudo loginctl enable-linger %s`, then run this again", u.Username, u.Username)
	}
	dir := filepath.Join(u.HomeDir, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	if err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return "", err
	}
	if err := run("systemctl", "--user", "enable", "--now", name); err != nil {
		return "", err
	}
	run("systemctl", "--user", "restart", name)
	return fmt.Sprintf("installed user service %s; logs: journalctl --user -u %s -f", name, unitName(role)) + lingerMsg, nil
}

func installLaunchd(role string, full []string) (string, error) {
	home, _ := os.UserHomeDir()
	label := "dev.herdr-ember-fleet." + role
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	logDir := filepath.Join(home, "Library", "Logs")
	os.MkdirAll(logDir, 0o755)
	var buf bytes.Buffer
	if err := launchdPlist.Execute(&buf, map[string]any{"Label": label, "Args": full, "Log": filepath.Join(logDir, "ember-fleet-"+role+".log")}); err != nil {
		return "", err
	}
	path := filepath.Join(dir, label+".plist")
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", domain+"/"+label).Run()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	if err := run("launchctl", "bootstrap", domain, path); err != nil {
		return "", err
	}
	return fmt.Sprintf("installed launchd agent %s (starts when you log in); log: ~/Library/Logs/ember-fleet-%s.log", label, role), nil
}

// Remove stops and deletes the service for role.
func Remove(role string, system bool) error {
	switch runtime.GOOS {
	case "linux":
		name := unitName(role) + ".service"
		if system {
			run("sudo", "systemctl", "disable", "--now", name)
			return run("sudo", "rm", "-f", "/etc/systemd/system/"+name)
		}
		run("systemctl", "--user", "disable", "--now", name)
		home, _ := os.UserHomeDir()
		os.Remove(filepath.Join(home, ".config", "systemd", "user", name))
		return run("systemctl", "--user", "daemon-reload")
	case "darwin":
		home, _ := os.UserHomeDir()
		label := "dev.herdr-ember-fleet." + role
		exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
		return os.Remove(filepath.Join(home, "Library", "LaunchAgents", label+".plist"))
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
