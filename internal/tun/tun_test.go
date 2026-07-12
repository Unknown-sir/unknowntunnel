//go:build linux

package tun

import "testing"

func TestPacketAllowedIPv4(t *testing.T) {
	tcp := make([]byte, 20)
	tcp[0] = 0x45
	tcp[9] = 6
	udp := append([]byte(nil), tcp...)
	udp[9] = 17
	if !PacketAllowed(tcp, []string{"tcp"}) || PacketAllowed(udp, []string{"tcp"}) {
		t.Fatal("TCP filter failed")
	}
	if !PacketAllowed(udp, []string{"udp"}) || PacketAllowed(tcp, []string{"udp"}) {
		t.Fatal("UDP filter failed")
	}
	if !PacketAllowed(tcp, []string{"tcp", "udp"}) || !PacketAllowed(udp, []string{"tcp", "udp"}) {
		t.Fatal("both filter failed")
	}
}

func TestPacketAllowedIPv6WithExtensionHeader(t *testing.T) {
	packet := make([]byte, 48)
	packet[0] = 0x60
	packet[6] = 0   // Hop-by-Hop Options
	packet[40] = 17 // UDP follows
	packet[41] = 0  // 8-byte extension header
	if !PacketAllowed(packet, []string{"udp"}) {
		t.Fatal("IPv6 UDP behind extension header should be allowed")
	}
	if PacketAllowed(packet, []string{"tcp"}) {
		t.Fatal("IPv6 UDP packet should not pass TCP-only filter")
	}
}

func TestRejectMalformedIPv4Header(t *testing.T) {
	packet := make([]byte, 20)
	packet[0] = 0x4f // claims a 60-byte header
	packet[9] = 6
	if PacketAllowed(packet, []string{"tcp"}) {
		t.Fatal("malformed IPv4 header should be rejected")
	}
}

func TestPacketAllowedIPv6Extension(t *testing.T) {
	packet := make([]byte, 48)
	packet[0] = 0x60
	packet[6] = 0 // hop-by-hop extension
	packet[40] = 17
	packet[41] = 0 // 8-byte extension
	if !PacketAllowed(packet, []string{"udp"}) {
		t.Fatal("IPv6 UDP behind extension header was not accepted")
	}
	if PacketAllowed(packet, []string{"tcp"}) {
		t.Fatal("IPv6 UDP packet was incorrectly accepted as TCP")
	}
}
