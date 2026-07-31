package service

import (
	"testing"
	"time"
)

// Loopback answers ICMP without leaving the machine, so this stays offline and
// deterministic while still exercising the real iphlpapi call — the part where
// a wrong struct layout or byte order would show up.
func TestICMPEchoLoopback(t *testing.T) {
	rtt := icmpEchoLatency("127.0.0.1", 2*time.Second)
	if rtt < 0 {
		t.Fatalf("icmpEchoLatency(127.0.0.1) = %d, want a round-trip time", rtt)
	}
	if rtt > 1000 {
		t.Errorf("loopback round trip of %d ms is implausible", rtt)
	}
}

// A host that cannot answer must report "unknown" rather than a made-up time.
func TestICMPEchoUnreachable(t *testing.T) {
	// 240.0.0.0/4 is reserved and has no route anywhere, so the call fails
	// outright. Documentation ranges are a poorer choice than they look: a
	// router on the path may answer for them, which it did while this was
	// being written.
	if rtt := icmpEchoLatency("240.0.0.1", 500*time.Millisecond); rtt != -1 {
		t.Errorf("icmpEchoLatency(unreachable) = %d, want -1", rtt)
	}
}

// A WireGuard node keeps its server inside the first peer and speaks only UDP,
// so both halves of PingServer's new behaviour have to line up for it to
// report anything at all — it used to return -1 unconditionally.
func TestPingServerTimesUDPOnlyNode(t *testing.T) {
	s := &AppService{}
	link := "wireguard://cHJpdmF0ZS1rZXktZm9yLXRoZS1uZW9ib3gtdGVzdHM=@127.0.0.1:51820" +
		"?address=172.16.0.2&publickey=cHVibGljLWtleS1mb3ItdGhlLW5lb2JveC10ZXN0cw%3D%3D#local"

	if rtt := s.PingServer(link); rtt < 0 {
		t.Errorf("PingServer(wireguard) = %d, want a round-trip time", rtt)
	}

	// A TCP protocol still measures the handshake, not the host: nothing
	// listens on this port, so the dial fails.
	if rtt := s.PingServer("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:1#dead"); rtt != -1 {
		t.Errorf("PingServer(closed tcp port) = %d, want -1", rtt)
	}
}

func TestResolveIPv4(t *testing.T) {
	ip, err := resolveIPv4("127.0.0.1", time.Second)
	if err != nil {
		t.Fatalf("resolveIPv4(127.0.0.1): %v", err)
	}
	if len(ip) != 4 || ip[0] != 127 || ip[3] != 1 {
		t.Errorf("resolveIPv4(127.0.0.1) = %v, want the four address bytes", ip)
	}

	// IPv6 needs a different API and is reported rather than silently timed.
	if _, err := resolveIPv4("::1", time.Second); err == nil {
		t.Error("resolveIPv4(::1) succeeded, want an error")
	}
}
