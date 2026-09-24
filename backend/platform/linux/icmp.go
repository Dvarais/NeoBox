package linux

import (
	"net"
	"os/exec"
	"time"
)

type Prober struct{}

func (p *Prober) Ping(host string, timeout time.Duration) int {
	// 1. Try TCP dial
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "80"), timeout)
	if err == nil {
		_ = conn.Close()
		return int(time.Since(start).Milliseconds())
	}

	// 2. Try unprivileged ping socket (udp4 datagram ICMP)
	pStart := time.Now()
	pConn, err := net.DialTimeout("udp4", net.JoinHostPort(host, "0"), timeout)
	if err == nil {
		_ = pConn.Close()
		return int(time.Since(pStart).Milliseconds())
	}

	// 3. Fallback to system ping
	if _, err := exec.LookPath("ping"); err == nil {
		cStart := time.Now()
		cmd := exec.Command("ping", "-c", "1", "-W", "1", host)
		if err := cmd.Run(); err == nil {
			return int(time.Since(cStart).Milliseconds())
		}
	}

	return -1
}
