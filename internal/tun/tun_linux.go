//go:build linux

package tun

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
)

const (
	tunSetIFF = 0x400454ca
	iffTUN    = 0x0001
	iffNoPI   = 0x1000
)

type Device struct {
	file *os.File
	name string
}

func Open(cfg config.L3Config) (*Device, error) {
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}
	var ifr [40]byte
	copy(ifr[:16], cfg.Interface)
	*(*uint16)(unsafe.Pointer(&ifr[16])) = iffTUN | iffNoPI
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(tunSetIFF), uintptr(unsafe.Pointer(&ifr[0])))
	if errno != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("TUNSETIFF: %w", errno)
	}
	actualName := strings.TrimRight(string(ifr[:16]), "\x00")
	dev := &Device{file: file, name: actualName}
	if err := configure(actualName, cfg); err != nil {
		_ = dev.Close()
		return nil, err
	}
	return dev, nil
}

func (d *Device) Name() string                { return d.name }
func (d *Device) Read(p []byte) (int, error)  { return d.file.Read(p) }
func (d *Device) Write(p []byte) (int, error) { return d.file.Write(p) }
func (d *Device) Close() error                { return d.file.Close() }

func configure(name string, cfg config.L3Config) error {
	if _, err := exec.LookPath("ip"); err != nil {
		return errors.New("the ip command is required (install iproute2)")
	}
	commands := [][]string{
		{"addr", "replace", cfg.Address, "dev", name},
		{"link", "set", "dev", name, "mtu", fmt.Sprint(cfg.MTU)},
		{"link", "set", "dev", name, "up"},
	}
	for _, route := range cfg.Routes {
		commands = append(commands, []string{"route", "replace", route, "dev", name})
	}
	for _, args := range commands {
		cmd := exec.Command("ip", args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ip %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func PacketAllowed(packet []byte, protocols []string) bool {
	proto, ok := ipProtocol(packet)
	if !ok {
		return false
	}
	for _, allowed := range protocols {
		switch allowed {
		case "tcp":
			if proto == 6 {
				return true
			}
		case "udp":
			if proto == 17 {
				return true
			}
		}
	}
	return false
}

func ipProtocol(packet []byte) (byte, bool) {
	if len(packet) < 1 {
		return 0, false
	}
	switch packet[0] >> 4 {
	case 4:
		if len(packet) < 20 {
			return 0, false
		}
		headerLen := int(packet[0]&0x0f) * 4
		if headerLen < 20 || len(packet) < headerLen {
			return 0, false
		}
		return packet[9], true
	case 6:
		return ipv6UpperLayerProtocol(packet)
	default:
		return 0, false
	}
}

func ipv6UpperLayerProtocol(packet []byte) (byte, bool) {
	if len(packet) < 40 {
		return 0, false
	}
	next := packet[6]
	offset := 40
	for headers := 0; headers < 16; headers++ {
		switch next {
		case 6, 17:
			return next, true
		case 0, 43, 60: // Hop-by-Hop, Routing, Destination Options
			if len(packet) < offset+2 {
				return 0, false
			}
			next = packet[offset]
			headerLen := (int(packet[offset+1]) + 1) * 8
			if headerLen < 8 || len(packet) < offset+headerLen {
				return 0, false
			}
			offset += headerLen
		case 44: // Fragment
			if len(packet) < offset+8 {
				return 0, false
			}
			next = packet[offset]
			offset += 8
		case 51: // Authentication Header
			if len(packet) < offset+2 {
				return 0, false
			}
			next = packet[offset]
			headerLen := (int(packet[offset+1]) + 2) * 4
			if headerLen < 8 || len(packet) < offset+headerLen {
				return 0, false
			}
			offset += headerLen
		case 50, 59: // ESP or No Next Header cannot be classified as TCP/UDP here.
			return next, true
		default:
			return next, true
		}
	}
	return 0, false
}
