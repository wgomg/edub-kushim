package config

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

func GenerateServiceFiles(configDir string) (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current user: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}

	if _, err := sanitizeUnitField(usr.Username); err != nil {
		return "", fmt.Errorf("invalid username %q: %w", usr.Username, err)
	}
	if _, err := sanitizeUnitField(usr.Gid); err != nil {
		return "", fmt.Errorf("invalid gid %q: %w", usr.Gid, err)
	}
	if _, err := sanitizeUnitField(homeDir); err != nil {
		return "", fmt.Errorf("invalid home dir %q: %w", homeDir, err)
	}

	kushimBin, err := resolveKushimBin()
	if err != nil {
		return "", err
	}

	if _, err := sanitizeUnitField(kushimBin); err != nil {
		return "", fmt.Errorf("invalid kushim binary path %q: %w", kushimBin, err)
	}

	edubBin := filepath.Join(filepath.Dir(kushimBin), "edub")

	svcDir := filepath.Join(configDir, "systemd")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		return "", fmt.Errorf("create systemd directory: %w", err)
	}

	hugotSvc := fmt.Sprintf(`[Unit]
Description=Kushim Hugot Tag Matcher Server
Documentation=https://github.com/wgomg/edub-kushim
PartOf=edub-kushim.target
After=network.target

[Service]
Type=notify
NotifyAccess=main
User=%s
Group=%s
Environment=HOME=%s
ExecStart=%s hugot --socket %s/.config/edub-kushim/kushim-hugot.sock
ExecStop=/bin/kill -s TERM $MAINPID
Restart=on-failure
RestartSec=5
TimeoutStartSec=120
NoNewPrivileges=yes
ProtectSystem=full
PrivateTmp=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, usr.Username, usr.Gid, homeDir, kushimBin, homeDir)

	queueSvc := fmt.Sprintf(`[Unit]
Description=Kushim Batch Queue Daemon
Documentation=https://github.com/wgomg/edub-kushim
PartOf=edub-kushim.target
After=network.target kushim-hugot.service
Wants=kushim-hugot.service

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
ExecStart=%s queue
ExecStop=/bin/kill -s TERM $MAINPID
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=full
PrivateTmp=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, usr.Username, usr.Gid, homeDir, kushimBin)

	edubSvc := fmt.Sprintf(`[Unit]
Description=Edub Document Management API Server
Documentation=https://github.com/wgomg/edub-kushim
PartOf=edub-kushim.target
After=network.target kushim-hugot.service
Wants=kushim-hugot.service

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
ExecStart=%s
ExecStop=/bin/kill -s TERM $MAINPID
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=full
PrivateTmp=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, usr.Username, usr.Gid, homeDir, edubBin)

	target := `[Unit]
Description=Edub Kushim Document Management System
Documentation=https://github.com/wgomg/edub-kushim
Wants=kushim-hugot.service kushim-queue.service edub.service
After=kushim-hugot.service kushim-queue.service edub.service

[Install]
WantedBy=multi-user.target
`

	files := map[string]string{
		"kushim-hugot.service":  hugotSvc,
		"kushim-queue.service":  queueSvc,
		"edub.service":          edubSvc,
		"edub-kushim.target":    target,
	}

	for name, content := range files {
		path := filepath.Join(svcDir, name)
		existing, err := os.ReadFile(path)
		if err == nil && string(existing) == content {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("write %s: %w", name, err)
		}
	}

	return svcDir, nil
}

func resolveKushimBin() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	base := filepath.Base(exe)
	if base == "kushim" || base == "kushim.exe" {
		return exe, nil
	}
	path, err := exec.LookPath("kushim")
	if err != nil {
		return "", fmt.Errorf("current executable is %s and 'kushim' not found on PATH: %w", exe, err)
	}
	return path, nil
}

func sanitizeUnitField(s string) (string, error) {
	if strings.ContainsAny(s, "\n\r") {
		return "", fmt.Errorf("value contains newline or carriage return: %q", s)
	}
	return s, nil
}
