package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var identifierRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var interfaceRE = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

type Config struct {
	NodeID    string             `json:"node_id"`
	PeerID    string             `json:"peer_id"`
	Role      string             `json:"role"`
	Auth      AuthConfig         `json:"auth"`
	Transport TransportConfig    `json:"transport"`
	L3        L3Config           `json:"l3"`
	Services  map[string]Service `json:"services"`
	Forwards  []Forward          `json:"forwards"`
}

type AuthConfig struct {
	SecretFile string `json:"secret_file"`
}

type TransportConfig struct {
	Mode       string `json:"mode"`
	Prefer     string `json:"prefer"`
	ListenTCP  string `json:"listen_tcp"`
	ListenUDP  string `json:"listen_udp"`
	ConnectTCP string `json:"connect_tcp"`
	ConnectUDP string `json:"connect_udp"`
}

type L3Config struct {
	Enabled        bool     `json:"enabled"`
	Interface      string   `json:"interface"`
	Address        string   `json:"address"`
	MTU            int      `json:"mtu"`
	Routes         []string `json:"routes"`
	AllowProtocols []string `json:"allow_protocols"`
}

type Service struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

type Forward struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Listen   string `json:"listen"`
	Service  string `json:"service"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return nil, errors.New("parse config: multiple JSON values are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config trailing data: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	var errs []error
	if !identifierRE.MatchString(c.NodeID) {
		errs = append(errs, errors.New("node_id must be 1-64 safe identifier characters"))
	}
	if !identifierRE.MatchString(c.PeerID) {
		errs = append(errs, errors.New("peer_id must be 1-64 safe identifier characters"))
	}
	if c.NodeID == c.PeerID && c.NodeID != "" {
		errs = append(errs, errors.New("node_id and peer_id must differ"))
	}
	c.Role = strings.ToLower(c.Role)
	if c.Role != "server" && c.Role != "client" {
		errs = append(errs, errors.New("role must be server or client"))
	}
	if c.Auth.SecretFile == "" {
		errs = append(errs, errors.New("auth.secret_file is required"))
	}
	if err := c.validateTransport(); err != nil {
		errs = append(errs, err)
	}
	if err := c.validateL3(); err != nil {
		errs = append(errs, err)
	}
	for name, svc := range c.Services {
		if !identifierRE.MatchString(name) {
			errs = append(errs, fmt.Errorf("service name %q is invalid", name))
			continue
		}
		svc.Network = strings.ToLower(svc.Network)
		if svc.Network != "tcp" && svc.Network != "udp" {
			errs = append(errs, fmt.Errorf("service %q network must be tcp or udp", name))
		}
		if err := validateDialAddress(svc.Address); err != nil {
			errs = append(errs, fmt.Errorf("service %q address: %w", name, err))
		}
		c.Services[name] = svc
	}
	seen := map[string]struct{}{}
	for i := range c.Forwards {
		f := &c.Forwards[i]
		f.Protocol = strings.ToLower(f.Protocol)
		if !identifierRE.MatchString(f.Name) {
			errs = append(errs, fmt.Errorf("forward[%d] name is invalid", i))
		}
		if _, ok := seen[f.Name]; ok {
			errs = append(errs, fmt.Errorf("duplicate forward name %q", f.Name))
		}
		seen[f.Name] = struct{}{}
		if f.Protocol != "tcp" && f.Protocol != "udp" {
			errs = append(errs, fmt.Errorf("forward %q protocol must be tcp or udp", f.Name))
		}
		if err := validateListenAddress(f.Listen); err != nil {
			errs = append(errs, fmt.Errorf("forward %q listen: %w", f.Name, err))
		}
		if !identifierRE.MatchString(f.Service) {
			errs = append(errs, fmt.Errorf("forward %q service is invalid", f.Name))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) validateTransport() error {
	var errs []error
	c.Transport.Mode = strings.ToLower(c.Transport.Mode)
	c.Transport.Prefer = strings.ToLower(c.Transport.Prefer)
	if c.Transport.Mode != "tcp" && c.Transport.Mode != "udp" && c.Transport.Mode != "both" {
		errs = append(errs, errors.New("transport.mode must be tcp, udp, or both"))
	}
	if c.Transport.Prefer == "" {
		if c.Transport.Mode == "both" {
			c.Transport.Prefer = "udp"
		} else {
			c.Transport.Prefer = c.Transport.Mode
		}
	}
	if c.Transport.Prefer != "tcp" && c.Transport.Prefer != "udp" {
		errs = append(errs, errors.New("transport.prefer must be tcp or udp"))
	}
	if c.Transport.Mode != "both" && c.Transport.Prefer != c.Transport.Mode {
		errs = append(errs, errors.New("transport.prefer must match transport.mode unless mode is both"))
	}
	needsTCP := c.Transport.Mode == "tcp" || c.Transport.Mode == "both"
	needsUDP := c.Transport.Mode == "udp" || c.Transport.Mode == "both"
	if c.Role == "server" {
		if needsTCP {
			if err := validateListenAddress(c.Transport.ListenTCP); err != nil {
				errs = append(errs, fmt.Errorf("transport.listen_tcp: %w", err))
			}
		}
		if needsUDP {
			if err := validateListenAddress(c.Transport.ListenUDP); err != nil {
				errs = append(errs, fmt.Errorf("transport.listen_udp: %w", err))
			}
		}
	} else if c.Role == "client" {
		if needsTCP {
			if err := validateDialAddress(c.Transport.ConnectTCP); err != nil {
				errs = append(errs, fmt.Errorf("transport.connect_tcp: %w", err))
			}
		}
		if needsUDP {
			if err := validateDialAddress(c.Transport.ConnectUDP); err != nil {
				errs = append(errs, fmt.Errorf("transport.connect_udp: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

func (c *Config) validateL3() error {
	if !c.L3.Enabled {
		return nil
	}
	var errs []error
	if !interfaceRE.MatchString(c.L3.Interface) {
		errs = append(errs, errors.New("l3.interface must be 1-15 safe interface characters"))
	}
	if _, _, err := net.ParseCIDR(c.L3.Address); err != nil {
		errs = append(errs, fmt.Errorf("l3.address: %w", err))
	}
	if c.L3.MTU == 0 {
		c.L3.MTU = 1200
	}
	if c.L3.MTU < 576 || c.L3.MTU > 1300 {
		errs = append(errs, errors.New("l3.mtu must be between 576 and 1300"))
	}
	for _, route := range c.L3.Routes {
		if _, _, err := net.ParseCIDR(route); err != nil {
			errs = append(errs, fmt.Errorf("l3 route %q: %w", route, err))
		}
	}
	if len(c.L3.AllowProtocols) == 0 {
		c.L3.AllowProtocols = []string{"tcp", "udp"}
	}
	seen := map[string]bool{}
	for i, p := range c.L3.AllowProtocols {
		p = strings.ToLower(p)
		c.L3.AllowProtocols[i] = p
		if p != "tcp" && p != "udp" {
			errs = append(errs, fmt.Errorf("l3.allow_protocols contains unsupported value %q", p))
		}
		if seen[p] {
			errs = append(errs, fmt.Errorf("l3.allow_protocols contains duplicate value %q", p))
		}
		seen[p] = true
	}
	return errors.Join(errs...)
}

func validateListenAddress(value string) error {
	_, _, err := splitAndValidateHostPort(value, false)
	return err
}

func validateDialAddress(value string) error {
	host, _, err := splitAndValidateHostPort(value, true)
	if err != nil {
		return err
	}
	if host == "0.0.0.0" || host == "::" {
		return errors.New("dial destination cannot be an unspecified address")
	}
	return nil
}

func splitAndValidateHostPort(value string, requireHost bool) (string, int, error) {
	if value == "" {
		return "", 0, errors.New("value is required")
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return "", 0, err
	}
	if strings.ContainsAny(host, "\r\n\t") {
		return "", 0, errors.New("host contains control characters")
	}
	if requireHost && strings.TrimSpace(host) == "" {
		return "", 0, errors.New("host is required")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("port must be a number from 1 to 65535")
	}
	return host, port, nil
}

func ReadSecret(path string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	secret := []byte(strings.TrimSpace(string(data)))
	if len(secret) < 32 {
		return nil, errors.New("secret must contain at least 32 characters")
	}
	if len(secret) > 4096 {
		return nil, errors.New("secret is unexpectedly large")
	}
	return secret, nil
}
