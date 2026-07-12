package admin

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

const (
	defaultConfigDir = "/etc/unknowntunnel"
	unitPrefix       = "unknowntunnel@"
	unitSuffix       = ".service"
)

type Manager struct {
	in         *bufio.Reader
	out        io.Writer
	errOut     io.Writer
	configDir  string
	systemctl  string
	journalctl string
	euid       func() int
}

func New() *Manager {
	configDir := os.Getenv("UNKNOWNTUNNEL_CONFIG_DIR")
	if configDir == "" {
		configDir = defaultConfigDir
	}
	systemctl := os.Getenv("UNKNOWNTUNNEL_SYSTEMCTL")
	if systemctl == "" {
		systemctl = "systemctl"
	}
	journalctl := os.Getenv("UNKNOWNTUNNEL_JOURNALCTL")
	if journalctl == "" {
		journalctl = "journalctl"
	}
	return &Manager{
		in:         bufio.NewReader(os.Stdin),
		out:        os.Stdout,
		errOut:     os.Stderr,
		configDir:  configDir,
		systemctl:  systemctl,
		journalctl: journalctl,
		euid:       os.Geteuid,
	}
}

func (m *Manager) Menu() error {
	if err := m.requireRoot(); err != nil {
		return err
	}
	if err := os.MkdirAll(m.configDir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	for {
		fmt.Fprintln(m.out, "\n========================================")
		fmt.Fprintln(m.out, "       Unknowntunnel Control Panel")
		fmt.Fprintln(m.out, "========================================")
		fmt.Fprintln(m.out, " 1) Create a tunnel")
		fmt.Fprintln(m.out, " 2) Edit a tunnel")
		fmt.Fprintln(m.out, " 3) List tunnels")
		fmt.Fprintln(m.out, " 4) Start a tunnel")
		fmt.Fprintln(m.out, " 5) Stop a tunnel")
		fmt.Fprintln(m.out, " 6) Restart a tunnel")
		fmt.Fprintln(m.out, " 7) Enable a tunnel at boot")
		fmt.Fprintln(m.out, " 8) Disable a tunnel at boot")
		fmt.Fprintln(m.out, " 9) Show tunnel status")
		fmt.Fprintln(m.out, "10) Show recent tunnel logs")
		fmt.Fprintln(m.out, "11) Show tunnel configuration")
		fmt.Fprintln(m.out, "12) Delete a tunnel")
		fmt.Fprintln(m.out, "13) Validate all configurations")
		fmt.Fprintln(m.out, " 0) Exit")
		choice, err := m.prompt("Select", "")
		if err != nil {
			return err
		}
		switch choice {
		case "1":
			if err := m.Setup(""); err != nil {
				m.printError(err)
			}
		case "2":
			name, err := m.selectInstance("Tunnel to edit")
			if err == nil {
				err = m.Edit(name)
			}
			if err != nil {
				m.printError(err)
			}
		case "3":
			if err := m.List(); err != nil {
				m.printError(err)
			}
		case "4", "5", "6", "7", "8":
			name, err := m.selectInstance("Tunnel")
			if err == nil {
				action := map[string]string{
					"4": "start", "5": "stop", "6": "restart", "7": "enable", "8": "disable",
				}[choice]
				err = m.Service(name, action)
			}
			if err != nil {
				m.printError(err)
			}
		case "9":
			name, err := m.selectInstance("Tunnel")
			if err == nil {
				err = m.Service(name, "status")
			}
			if err != nil {
				m.printError(err)
			}
		case "10":
			name, err := m.selectInstance("Tunnel")
			if err == nil {
				err = m.Logs(name, 100, false)
			}
			if err != nil {
				m.printError(err)
			}
		case "11":
			name, err := m.selectInstance("Tunnel")
			if err == nil {
				err = m.Show(name)
			}
			if err != nil {
				m.printError(err)
			}
		case "12":
			name, err := m.selectInstance("Tunnel to delete")
			if err == nil {
				err = m.Delete(name, false)
			}
			if err != nil {
				m.printError(err)
			}
		case "13":
			if err := m.ValidateAll(); err != nil {
				m.printError(err)
			}
		case "0", "q", "quit", "exit":
			return nil
		default:
			fmt.Fprintln(m.errOut, "Invalid selection.")
		}
	}
}

func (m *Manager) Setup(instance string) error {
	if err := m.requireRoot(); err != nil {
		return err
	}
	if err := os.MkdirAll(m.configDir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if instance == "" {
		var err error
		instance, err = m.promptIdentifier("Instance name", "tunnel1")
		if err != nil {
			return err
		}
	}
	if !config.ValidIdentifier(instance) {
		return errors.New("instance name must be a safe 1-64 character identifier")
	}
	path := m.configPath(instance)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("instance %q already exists; use edit instead", instance)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cfg, generatedSecret, err := m.wizard(instance, nil)
	if err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(m.out, "\nConfiguration saved to %s\n", path)
	if generatedSecret != "" {
		fmt.Fprintln(m.out, "\nIMPORTANT: copy this shared secret securely to the peer. It is shown once:")
		fmt.Fprintln(m.out, generatedSecret)
		fmt.Fprintln(m.out)
	}
	if err := m.daemonReload(); err != nil {
		return err
	}
	start, err := m.yesNo("Enable at boot and start this tunnel now", true)
	if err != nil {
		return err
	}
	if start {
		if err := m.runSystemctl("enable", "--now", m.unit(instance)); err != nil {
			fmt.Fprintf(m.errOut, "The configuration was saved, but the service did not start: %v\n", err)
			_ = m.Service(instance, "status")
			return nil
		}
		fmt.Fprintf(m.out, "Tunnel %q is enabled and started.\n", instance)
	} else {
		fmt.Fprintf(m.out, "Tunnel %q was created but not started.\n", instance)
	}
	return nil
}

func (m *Manager) Edit(instance string) error {
	if err := m.requireRoot(); err != nil {
		return err
	}
	path := m.configPath(instance)
	current, err := config.Load(path)
	if err != nil {
		return err
	}
	active := m.isActive(instance)
	cfg, generatedSecret, err := m.wizard(instance, current)
	if err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(m.out, "Configuration %q updated.\n", instance)
	if generatedSecret != "" {
		fmt.Fprintln(m.out, "\nIMPORTANT: update the peer with this new shared secret:")
		fmt.Fprintln(m.out, generatedSecret)
		fmt.Fprintln(m.out)
	}
	if active {
		restart, err := m.yesNo("Restart the active service with the new configuration", true)
		if err != nil {
			return err
		}
		if restart {
			return m.Service(instance, "restart")
		}
	} else {
		start, err := m.yesNo("Enable at boot and start this tunnel now", false)
		if err != nil {
			return err
		}
		if start {
			return m.runSystemctl("enable", "--now", m.unit(instance))
		}
	}
	return nil
}

func (m *Manager) List() error {
	instances, err := m.instances()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		fmt.Fprintln(m.out, "No tunnel instances are configured.")
		return nil
	}
	w := tabwriter.NewWriter(m.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "INSTANCE\tROLE\tTRANSPORT\tL3\tACTIVE\tENABLED\tCONFIG")
	for _, name := range instances {
		cfg, loadErr := config.Load(m.configPath(name))
		role, mode, l3 := "invalid", "-", "-"
		if loadErr == nil {
			role = cfg.Role
			mode = cfg.Transport.Mode
			if cfg.L3.Enabled {
				l3 = cfg.L3.Interface + " " + cfg.L3.Address
			} else {
				l3 = "disabled"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			name, role, mode, l3, m.serviceState(name, "is-active"), m.serviceState(name, "is-enabled"), m.configPath(name))
	}
	return w.Flush()
}

func (m *Manager) Show(instance string) error {
	cfg, err := config.Load(m.configPath(instance))
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(m.out, string(data))
	return nil
}

func (m *Manager) ValidateAll() error {
	instances, err := m.instances()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		fmt.Fprintln(m.out, "No configurations found.")
		return nil
	}
	var all []error
	for _, name := range instances {
		cfg, err := config.Load(m.configPath(name))
		if err == nil {
			_, err = config.ReadSecret(cfg.Auth.SecretFile)
		}
		if err != nil {
			fmt.Fprintf(m.errOut, "[FAILED] %s: %v\n", name, err)
			all = append(all, fmt.Errorf("%s: %w", name, err))
		} else {
			fmt.Fprintf(m.out, "[OK] %s\n", name)
		}
	}
	return errors.Join(all...)
}

func (m *Manager) Service(instance, action string) error {
	if err := m.requireRoot(); err != nil {
		return err
	}
	if !config.ValidIdentifier(instance) {
		return errors.New("invalid instance name")
	}
	if _, err := os.Stat(m.configPath(instance)); err != nil {
		return fmt.Errorf("configuration for %q does not exist", instance)
	}
	switch action {
	case "start", "stop", "restart":
		if err := m.runSystemctl(action, m.unit(instance)); err != nil {
			return err
		}
		fmt.Fprintf(m.out, "%s: %s completed.\n", instance, action)
		return nil
	case "enable", "disable":
		if err := m.runSystemctl(action, m.unit(instance)); err != nil {
			return err
		}
		fmt.Fprintf(m.out, "%s: %s completed.\n", instance, action)
		return nil
	case "status":
		cmd := exec.Command(m.systemctl, "status", "--no-pager", "--full", m.unit(instance))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, m.out, m.errOut
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
}

func (m *Manager) Logs(instance string, lines int, follow bool) error {
	if !config.ValidIdentifier(instance) {
		return errors.New("invalid instance name")
	}
	if lines <= 0 {
		lines = 100
	}
	args := []string{"-u", m.unit(instance), "--no-pager", "-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.Command(m.journalctl, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, m.out, m.errOut
	return cmd.Run()
}

func (m *Manager) Delete(instance string, force bool) error {
	if err := m.requireRoot(); err != nil {
		return err
	}
	cfg, err := config.Load(m.configPath(instance))
	if err != nil {
		return err
	}
	if !force {
		ok, err := m.yesNo(fmt.Sprintf("Stop and permanently delete tunnel %q", instance), false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(m.out, "Deletion cancelled.")
			return nil
		}
	}
	_ = m.runSystemctl("disable", "--now", m.unit(instance))
	if err := os.Remove(m.configPath(instance)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	removeSecret := false
	if !m.secretReferencedElsewhere(cfg.Auth.SecretFile, instance) {
		if force {
			removeSecret = false
		} else {
			removeSecret, err = m.yesNo("Delete this tunnel's secret file too", false)
			if err != nil {
				return err
			}
		}
	}
	if removeSecret {
		_ = os.Remove(cfg.Auth.SecretFile)
	}
	fmt.Fprintf(m.out, "Tunnel %q deleted.\n", instance)
	return nil
}

func (m *Manager) wizard(instance string, current *config.Config) (*config.Config, string, error) {
	cfg := defaultConfig(instance)
	if current != nil {
		copyData, _ := json.Marshal(current)
		_ = json.Unmarshal(copyData, cfg)
	}
	fmt.Fprintf(m.out, "\n--- Configure tunnel %s ---\n", instance)

	role, err := m.choice("Node role", []string{"server", "client"}, cfg.Role)
	if err != nil {
		return nil, "", err
	}
	cfg.Role = role
	cfg.NodeID, err = m.promptIdentifier("Local node ID", cfg.NodeID)
	if err != nil {
		return nil, "", err
	}
	cfg.PeerID, err = m.promptIdentifier("Peer node ID", cfg.PeerID)
	if err != nil {
		return nil, "", err
	}

	cfg.Transport.Mode, err = m.choice("Outer transport", []string{"tcp", "udp", "both"}, cfg.Transport.Mode)
	if err != nil {
		return nil, "", err
	}
	if cfg.Transport.Mode == "both" {
		cfg.Transport.Prefer, err = m.choice("Preferred transport", []string{"udp", "tcp"}, cfg.Transport.Prefer)
		if err != nil {
			return nil, "", err
		}
	} else {
		cfg.Transport.Prefer = cfg.Transport.Mode
	}
	if err := m.configureTransport(cfg); err != nil {
		return nil, "", err
	}
	if err := m.configureL3(instance, cfg); err != nil {
		return nil, "", err
	}
	if err := m.configureServices(cfg); err != nil {
		return nil, "", err
	}
	if err := m.configureForwards(cfg); err != nil {
		return nil, "", err
	}
	generatedSecret, err := m.configureSecret(instance, cfg, current != nil)
	if err != nil {
		return nil, "", err
	}
	if err := cfg.Validate(); err != nil {
		return nil, "", fmt.Errorf("generated configuration is invalid: %w", err)
	}
	return cfg, generatedSecret, nil
}

func (m *Manager) configureTransport(cfg *config.Config) error {
	needsTCP := cfg.Transport.Mode == "tcp" || cfg.Transport.Mode == "both"
	needsUDP := cfg.Transport.Mode == "udp" || cfg.Transport.Mode == "both"
	oldListenTCP, oldListenUDP := cfg.Transport.ListenTCP, cfg.Transport.ListenUDP
	oldConnectTCP, oldConnectUDP := cfg.Transport.ConnectTCP, cfg.Transport.ConnectUDP
	cfg.Transport.ListenTCP, cfg.Transport.ListenUDP = "", ""
	cfg.Transport.ConnectTCP, cfg.Transport.ConnectUDP = "", ""
	if cfg.Role == "server" {
		host, port := "0.0.0.0", "8443"
		if oldHost, oldPort, ok := firstEndpoint(oldListenTCP, oldListenUDP); ok {
			host, port = oldHost, oldPort
		}
		var err error
		host, err = m.prompt("Listen address", host)
		if err != nil {
			return err
		}
		port, err = m.promptPort("Transport port", port)
		if err != nil {
			return err
		}
		separate := false
		if cfg.Transport.Mode == "both" {
			separate, err = m.yesNo("Use separate TCP and UDP ports", false)
			if err != nil {
				return err
			}
		}
		if needsTCP {
			cfg.Transport.ListenTCP = net.JoinHostPort(host, port)
		}
		if needsUDP {
			udpPort := port
			if separate {
				udpPort, err = m.promptPort("UDP transport port", port)
				if err != nil {
					return err
				}
			}
			cfg.Transport.ListenUDP = net.JoinHostPort(host, udpPort)
		}
		return nil
	}

	host, port := "", "8443"
	if oldHost, oldPort, ok := firstEndpoint(oldConnectTCP, oldConnectUDP); ok {
		host, port = oldHost, oldPort
	}
	var err error
	host, err = m.promptRequired("Peer public IP or hostname", host)
	if err != nil {
		return err
	}
	port, err = m.promptPort("Peer transport port", port)
	if err != nil {
		return err
	}
	separate := false
	if cfg.Transport.Mode == "both" {
		separate, err = m.yesNo("Use a separate UDP peer port", false)
		if err != nil {
			return err
		}
	}
	if needsTCP {
		cfg.Transport.ConnectTCP = net.JoinHostPort(host, port)
	}
	if needsUDP {
		udpPort := port
		if separate {
			udpPort, err = m.promptPort("Peer UDP transport port", port)
			if err != nil {
				return err
			}
		}
		cfg.Transport.ConnectUDP = net.JoinHostPort(host, udpPort)
	}
	return nil
}

func (m *Manager) configureSecret(instance string, cfg *config.Config, editing bool) (string, error) {
	defaultPath := filepath.Join(m.configDir, instance+".key")
	if cfg.Auth.SecretFile == "" {
		cfg.Auth.SecretFile = defaultPath
	}
	options := []string{"generate", "paste", "file"}
	defaultChoice := "generate"
	if editing {
		options = append([]string{"keep"}, options...)
		defaultChoice = "keep"
	}
	choice, err := m.choice("Shared secret action", options, defaultChoice)
	if err != nil {
		return "", err
	}
	switch choice {
	case "keep":
		if _, err := config.ReadSecret(cfg.Auth.SecretFile); err != nil {
			return "", fmt.Errorf("current secret is not usable: %w", err)
		}
		return "", nil
	case "file":
		path, err := m.promptRequired("Existing secret file", cfg.Auth.SecretFile)
		if err != nil {
			return "", err
		}
		if _, err := config.ReadSecret(path); err != nil {
			return "", err
		}
		cfg.Auth.SecretFile = path
		return "", nil
	case "paste":
		secret, err := m.readSecret("Paste the shared secret")
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(secret)) < 32 {
			return "", errors.New("secret must contain at least 32 characters")
		}
		path, err := m.promptRequired("Secret file path", defaultPath)
		if err != nil {
			return "", err
		}
		if err := config.WriteSecret(path, strings.TrimSpace(secret), true); err != nil {
			return "", err
		}
		cfg.Auth.SecretFile = path
		return "", nil
	case "generate":
		secret, err := randomSecret()
		if err != nil {
			return "", err
		}
		path, err := m.promptRequired("Secret file path", defaultPath)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(path); statErr == nil {
			overwrite, err := m.yesNo("Secret file exists; replace it", false)
			if err != nil {
				return "", err
			}
			if !overwrite {
				return "", errors.New("secret generation cancelled")
			}
		}
		if err := config.WriteSecret(path, secret, true); err != nil {
			return "", err
		}
		cfg.Auth.SecretFile = path
		return secret, nil
	default:
		return "", errors.New("unsupported secret action")
	}
}

func (m *Manager) configureL3(instance string, cfg *config.Config) error {
	enabled, err := m.yesNo("Enable Layer 3 TUN", cfg.L3.Enabled)
	if err != nil {
		return err
	}
	cfg.L3.Enabled = enabled
	if !enabled {
		cfg.L3.Interface = ""
		cfg.L3.Address = ""
		cfg.L3.MTU = 0
		cfg.L3.Routes = nil
		cfg.L3.AllowProtocols = nil
		return nil
	}
	defaultInterface := cfg.L3.Interface
	if defaultInterface == "" {
		defaultInterface = m.nextInterface(instance)
	}
	cfg.L3.Interface, err = m.promptInterface("TUN interface", defaultInterface)
	if err != nil {
		return err
	}
	defaultAddress := cfg.L3.Address
	if defaultAddress == "" {
		if cfg.Role == "server" {
			defaultAddress = "10.77.0.2/30"
		} else {
			defaultAddress = "10.77.0.1/30"
		}
	}
	cfg.L3.Address, err = m.promptCIDR("Local tunnel address", defaultAddress)
	if err != nil {
		return err
	}
	if cfg.L3.MTU == 0 {
		cfg.L3.MTU = 1200
	}
	cfg.L3.MTU, err = m.promptInt("TUN MTU", cfg.L3.MTU, 576, 1300)
	if err != nil {
		return err
	}
	packetDefault := protocolsChoice(cfg.L3.AllowProtocols)
	packetMode, err := m.choice("Layer 3 packet types", []string{"tcp", "udp", "both"}, packetDefault)
	if err != nil {
		return err
	}
	if packetMode == "both" {
		cfg.L3.AllowProtocols = []string{"tcp", "udp"}
	} else {
		cfg.L3.AllowProtocols = []string{packetMode}
	}
	routesDefault := strings.Join(cfg.L3.Routes, ",")
	routesText, err := m.prompt("Routes through this TUN, comma separated (blank for none)", routesDefault)
	if err != nil {
		return err
	}
	cfg.L3.Routes = splitCSV(routesText)
	for _, route := range cfg.L3.Routes {
		if _, _, err := net.ParseCIDR(route); err != nil {
			return fmt.Errorf("invalid route %q: %w", route, err)
		}
	}
	return nil
}

func (m *Manager) configureServices(cfg *config.Config) error {
	configureDefault := cfg.Role == "server" || len(cfg.Services) > 0
	configure, err := m.yesNo("Configure destination services available to the peer", configureDefault)
	if err != nil {
		return err
	}
	if !configure {
		cfg.Services = map[string]config.Service{}
		return nil
	}
	names := sortedServiceNames(cfg.Services)
	count, err := m.promptInt("Number of destination services", len(names), 0, 256)
	if err != nil {
		return err
	}
	services := make(map[string]config.Service, count)
	for i := 0; i < count; i++ {
		oldName := ""
		old := config.Service{Network: "tcp", Address: "127.0.0.1:443"}
		if i < len(names) {
			oldName = names[i]
			old = cfg.Services[oldName]
		}
		name, err := m.promptIdentifier(fmt.Sprintf("Service %d name", i+1), valueOr(oldName, fmt.Sprintf("service-%d", i+1)))
		if err != nil {
			return err
		}
		if _, exists := services[name]; exists {
			return fmt.Errorf("duplicate service name %q", name)
		}
		network, err := m.choice("Service protocol", []string{"tcp", "udp"}, valueOr(old.Network, "tcp"))
		if err != nil {
			return err
		}
		address, err := m.promptRequired("Destination address (host:port)", valueOr(old.Address, "127.0.0.1:443"))
		if err != nil {
			return err
		}
		services[name] = config.Service{Network: network, Address: address}
	}
	cfg.Services = services
	return nil
}

func (m *Manager) configureForwards(cfg *config.Config) error {
	configureDefault := cfg.Role == "client" || len(cfg.Forwards) > 0
	configure, err := m.yesNo("Configure local Layer 4 forwards", configureDefault)
	if err != nil {
		return err
	}
	if !configure {
		cfg.Forwards = nil
		return nil
	}
	count, err := m.promptInt("Number of local forwards", len(cfg.Forwards), 0, 256)
	if err != nil {
		return err
	}
	forwards := make([]config.Forward, 0, count)
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		old := config.Forward{Protocol: "tcp", Listen: "0.0.0.0:443", Service: "service-1"}
		if i < len(cfg.Forwards) {
			old = cfg.Forwards[i]
		}
		name, err := m.promptIdentifier(fmt.Sprintf("Forward %d name", i+1), valueOr(old.Name, fmt.Sprintf("forward-%d", i+1)))
		if err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate forward name %q", name)
		}
		seen[name] = struct{}{}
		protocolName, err := m.choice("Forward protocol", []string{"tcp", "udp"}, valueOr(old.Protocol, "tcp"))
		if err != nil {
			return err
		}
		listen, err := m.promptRequired("Local listen address", valueOr(old.Listen, "0.0.0.0:443"))
		if err != nil {
			return err
		}
		service, err := m.promptIdentifier("Remote service name", valueOr(old.Service, "service-1"))
		if err != nil {
			return err
		}
		forwards = append(forwards, config.Forward{Name: name, Protocol: protocolName, Listen: listen, Service: service})
	}
	cfg.Forwards = forwards
	return nil
}

func defaultConfig(instance string) *config.Config {
	return &config.Config{
		NodeID: instance,
		PeerID: "peer",
		Role:   "client",
		Transport: config.TransportConfig{
			Mode:   "both",
			Prefer: "udp",
		},
		L3: config.L3Config{
			Enabled:        true,
			MTU:            1200,
			AllowProtocols: []string{"tcp", "udp"},
		},
		Services: map[string]config.Service{},
		Forwards: []config.Forward{},
	}
}

func (m *Manager) instances() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(m.configDir, "*.json"))
	if err != nil {
		return nil, err
	}
	instances := make([]string, 0, len(matches))
	for _, match := range matches {
		name := strings.TrimSuffix(filepath.Base(match), ".json")
		if config.ValidIdentifier(name) {
			instances = append(instances, name)
		}
	}
	sort.Strings(instances)
	return instances, nil
}

func (m *Manager) selectInstance(label string) (string, error) {
	instances, err := m.instances()
	if err != nil {
		return "", err
	}
	if len(instances) == 0 {
		return "", errors.New("no tunnel instances are configured")
	}
	fmt.Fprintln(m.out)
	for i, name := range instances {
		fmt.Fprintf(m.out, "%d) %s [%s]\n", i+1, name, m.serviceState(name, "is-active"))
	}
	for {
		value, err := m.prompt(label, "")
		if err != nil {
			return "", err
		}
		if index, convErr := strconv.Atoi(value); convErr == nil && index >= 1 && index <= len(instances) {
			return instances[index-1], nil
		}
		if config.ValidIdentifier(value) {
			for _, name := range instances {
				if value == name {
					return name, nil
				}
			}
		}
		fmt.Fprintln(m.errOut, "Select an existing number or instance name.")
	}
}

func (m *Manager) configPath(instance string) string {
	return filepath.Join(m.configDir, instance+".json")
}

func (m *Manager) unit(instance string) string {
	return unitPrefix + instance + unitSuffix
}

func (m *Manager) requireRoot() error {
	if m.euid() != 0 {
		return errors.New("administrative commands must be run as root; use sudo unknowntunnel")
	}
	return nil
}

func (m *Manager) daemonReload() error {
	return m.runSystemctl("daemon-reload")
}

func (m *Manager) runSystemctl(args ...string) error {
	cmd := exec.Command(m.systemctl, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, m.out, m.errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (m *Manager) serviceState(instance, action string) string {
	cmd := exec.Command(m.systemctl, action, m.unit(instance))
	out, err := cmd.Output()
	state := strings.TrimSpace(string(out))
	if state != "" {
		return state
	}
	if err != nil {
		return "unknown"
	}
	return "yes"
}

func (m *Manager) isActive(instance string) bool {
	return exec.Command(m.systemctl, "is-active", "--quiet", m.unit(instance)).Run() == nil
}

func (m *Manager) secretReferencedElsewhere(path, excluded string) bool {
	instances, _ := m.instances()
	for _, instance := range instances {
		if instance == excluded {
			continue
		}
		cfg, err := config.Load(m.configPath(instance))
		if err == nil && filepath.Clean(cfg.Auth.SecretFile) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

func (m *Manager) nextInterface(excluded string) string {
	used := map[string]bool{}
	instances, _ := m.instances()
	for _, instance := range instances {
		if instance == excluded {
			continue
		}
		cfg, err := config.Load(m.configPath(instance))
		if err == nil && cfg.L3.Interface != "" {
			used[cfg.L3.Interface] = true
		}
	}
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("utun%d", i)
		if !used[name] {
			return name
		}
	}
	return "utun0"
}

func (m *Manager) prompt(label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(m.out, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(m.out, "%s: ", label)
	}
	line, err := m.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = defaultValue
	}
	return line, nil
}

func (m *Manager) promptRequired(label, defaultValue string) (string, error) {
	for {
		value, err := m.prompt(label, defaultValue)
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
		fmt.Fprintln(m.errOut, "A value is required.")
	}
}

func (m *Manager) promptIdentifier(label, defaultValue string) (string, error) {
	for {
		value, err := m.promptRequired(label, defaultValue)
		if err != nil {
			return "", err
		}
		if config.ValidIdentifier(value) {
			return value, nil
		}
		fmt.Fprintln(m.errOut, "Use 1-64 letters, numbers, dot, underscore or hyphen; start with a letter or number.")
	}
}

func (m *Manager) promptInterface(label, defaultValue string) (string, error) {
	for {
		value, err := m.promptRequired(label, defaultValue)
		if err != nil {
			return "", err
		}
		if config.ValidInterface(value) {
			return value, nil
		}
		fmt.Fprintln(m.errOut, "Interface names must contain 1-15 safe characters.")
	}
}

func (m *Manager) promptCIDR(label, defaultValue string) (string, error) {
	for {
		value, err := m.promptRequired(label, defaultValue)
		if err != nil {
			return "", err
		}
		if _, _, err := net.ParseCIDR(value); err == nil {
			return value, nil
		}
		fmt.Fprintln(m.errOut, "Enter a valid IPv4 or IPv6 CIDR, for example 10.77.0.1/30.")
	}
}

func (m *Manager) promptPort(label, defaultValue string) (string, error) {
	for {
		value, err := m.promptRequired(label, defaultValue)
		if err != nil {
			return "", err
		}
		port, convErr := strconv.Atoi(value)
		if convErr == nil && port >= 1 && port <= 65535 {
			return value, nil
		}
		fmt.Fprintln(m.errOut, "Port must be from 1 to 65535.")
	}
}

func (m *Manager) promptInt(label string, defaultValue, minValue, maxValue int) (int, error) {
	for {
		value, err := m.prompt(label, strconv.Itoa(defaultValue))
		if err != nil {
			return 0, err
		}
		n, convErr := strconv.Atoi(value)
		if convErr == nil && n >= minValue && n <= maxValue {
			return n, nil
		}
		fmt.Fprintf(m.errOut, "Enter a number from %d to %d.\n", minValue, maxValue)
	}
}

func (m *Manager) yesNo(label string, defaultValue bool) (bool, error) {
	promptSuffix := "Y/n"
	if !defaultValue {
		promptSuffix = "y/N"
	}
	for {
		fmt.Fprintf(m.out, "%s [%s]: ", label, promptSuffix)
		line, err := m.in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		value := strings.ToLower(strings.TrimSpace(line))
		if value == "" {
			return defaultValue, nil
		}
		switch value {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(m.errOut, "Enter y or n.")
		}
	}
}

func (m *Manager) choice(label string, options []string, defaultValue string) (string, error) {
	for {
		value, err := m.prompt(label+" ("+strings.Join(options, "/")+")", defaultValue)
		if err != nil {
			return "", err
		}
		value = strings.ToLower(value)
		for _, option := range options {
			if value == option {
				return value, nil
			}
		}
		fmt.Fprintf(m.errOut, "Choose one of: %s.\n", strings.Join(options, ", "))
	}
}

func (m *Manager) readSecret(label string) (string, error) {
	fmt.Fprintf(m.out, "%s: ", label)
	hidden := false
	if _, err := exec.LookPath("stty"); err == nil {
		disable := exec.Command("stty", "-echo")
		disable.Stdin = os.Stdin
		if disable.Run() == nil {
			hidden = true
			defer func() {
				restore := exec.Command("stty", "echo")
				restore.Stdin = os.Stdin
				_ = restore.Run()
				fmt.Fprintln(m.out)
			}()
		}
	}
	line, err := m.in.ReadString('\n')
	if !hidden {
		fmt.Fprintln(m.out)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (m *Manager) printError(err error) {
	fmt.Fprintf(m.errOut, "Error: %v\n", err)
}

func firstEndpoint(values ...string) (string, string, bool) {
	for _, value := range values {
		if value == "" {
			continue
		}
		host, port, err := net.SplitHostPort(value)
		if err == nil {
			return host, port, true
		}
	}
	return "", "", false
}

func protocolsChoice(protocols []string) string {
	seenTCP, seenUDP := false, false
	for _, p := range protocols {
		switch strings.ToLower(p) {
		case "tcp":
			seenTCP = true
		case "udp":
			seenUDP = true
		}
	}
	if seenTCP && seenUDP {
		return "both"
	}
	if seenUDP {
		return "udp"
	}
	return "tcp"
}

func sortedServiceNames(services map[string]config.Service) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func randomSecret() (string, error) {
	random := make([]byte, 48)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}
