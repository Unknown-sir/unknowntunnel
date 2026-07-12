package config

import (
	"os"
	"testing"
)

func TestValidateMinimalClient(t *testing.T) {
	cfg := Config{
		NodeID: "iran",
		PeerID: "kharej",
		Role:   "client",
		Auth:   AuthConfig{SecretFile: "/tmp/key"},
		Transport: TransportConfig{
			Mode:       "both",
			Prefer:     "udp",
			ConnectTCP: "192.0.2.1:8443",
			ConnectUDP: "192.0.2.1:8443",
		},
		L3: L3Config{Enabled: true, Interface: "utun0", Address: "10.77.0.1/30", MTU: 1200},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.L3.AllowProtocols) != 2 {
		t.Fatalf("expected default protocols, got %v", cfg.L3.AllowProtocols)
	}
}

func TestRejectOpenEndedTransport(t *testing.T) {
	cfg := Config{NodeID: "a", PeerID: "b", Role: "server", Auth: AuthConfig{SecretFile: "/tmp/key"}, Transport: TransportConfig{Mode: "tcp"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRejectInvalidDialAddress(t *testing.T) {
	cfg := Config{
		NodeID: "iran", PeerID: "kharej", Role: "client",
		Auth:      AuthConfig{SecretFile: "/tmp/key"},
		Transport: TransportConfig{Mode: "tcp", Prefer: "tcp", ConnectTCP: ":8443"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected empty dial host to be rejected")
	}

	cfg.Transport.ConnectTCP = "192.0.2.1:not-a-port"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-numeric port to be rejected")
	}
}

func TestExampleConfigurations(t *testing.T) {
	for _, path := range []string{"../../examples/server.json", "../../examples/client.json"} {
		if _, err := Load(path); err != nil {
			t.Fatalf("example %s is invalid: %v", path, err)
		}
	}
}

func TestSaveAndWriteSecretPermissions(t *testing.T) {
	dir := t.TempDir()
	secretPath := dir + "/demo.key"
	if err := WriteSecret(secretPath, "01234567890123456789012345678901", false); err != nil {
		t.Fatal(err)
	}
	secretInfo, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := secretInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("secret mode = %o, want 600", got)
	}

	cfg := Config{
		NodeID: "iran",
		PeerID: "kharej",
		Role:   "client",
		Auth:   AuthConfig{SecretFile: secretPath},
		Transport: TransportConfig{
			Mode:       "udp",
			Prefer:     "udp",
			ConnectUDP: "192.0.2.1:8443",
		},
		L3: L3Config{Enabled: false},
	}
	configPath := dir + "/demo.json"
	if err := Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
	loaded, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Transport.Mode != "udp" {
		t.Fatalf("mode = %q, want udp", loaded.Transport.Mode)
	}
}
