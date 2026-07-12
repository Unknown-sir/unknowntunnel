package admin

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

func TestSetupCreatesConfigSecretAndReloadsSystemd(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	fakeSystemctl := filepath.Join(dir, "systemctl")
	script := "#!/bin/sh\necho \"$@\" >> " + logPath + "\nexit 0\n"
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	answers := strings.Join([]string{
		"server",       // role
		"kharej",       // node ID
		"iran",         // peer ID
		"udp",          // transport
		"",             // listen address
		"9000",         // transport port
		"",             // enable L3
		"",             // interface
		"",             // tunnel address
		"",             // MTU
		"",             // packet types
		"",             // routes
		"",             // configure services
		"1",            // service count
		"dns",          // service name
		"udp",          // service protocol
		"127.0.0.1:53", // service address
		"n",            // no local forwards
		"",             // generate secret
		"",             // secret path
		"n",            // do not start
	}, "\n") + "\n"

	var out bytes.Buffer
	m := &Manager{
		in:         bufio.NewReader(strings.NewReader(answers)),
		out:        &out,
		errOut:     &out,
		configDir:  dir,
		systemctl:  fakeSystemctl,
		journalctl: "journalctl",
		euid:       func() int { return 0 },
	}
	if err := m.Setup("demo"); err != nil {
		t.Fatalf("Setup() error = %v\noutput:\n%s", err, out.String())
	}

	cfg, err := config.Load(filepath.Join(dir, "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != "server" || cfg.Transport.Mode != "udp" || cfg.Transport.ListenUDP != "0.0.0.0:9000" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.L3.AllowProtocols[0] != "tcp" || len(cfg.L3.AllowProtocols) != 2 {
		t.Fatalf("unexpected L3 protocols: %v", cfg.L3.AllowProtocols)
	}
	if svc := cfg.Services["dns"]; svc.Network != "udp" || svc.Address != "127.0.0.1:53" {
		t.Fatalf("unexpected service: %+v", svc)
	}
	if _, err := config.ReadSecret(filepath.Join(dir, "demo.key")); err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "daemon-reload") {
		t.Fatalf("daemon-reload not called: %s", logData)
	}
	if strings.Contains(string(logData), "enable --now") {
		t.Fatalf("service unexpectedly started: %s", logData)
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV("10.0.0.0/24, 192.168.1.0/24,,")
	if len(got) != 2 || got[0] != "10.0.0.0/24" || got[1] != "192.168.1.0/24" {
		t.Fatalf("splitCSV() = %#v", got)
	}
}
