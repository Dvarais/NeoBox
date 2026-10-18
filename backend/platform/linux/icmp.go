package linux

import (
	"context"
	"net"
	"os/exec"
	"strconv"
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

	// 2. Real ICMP echo via system ping with context deadline
	if pingPath, err := exec.LookPath("ping"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		timeoutSec := strconv.Itoa(int(timeout.Seconds()))
		if timeoutSec == "0" {
			timeoutSec = "1"
		}
		cStart := time.Now()
		cmd := exec.CommandContext(ctx, pingPath, "-c", "1", "-W", timeoutSec, host)
		if err := cmd.Run(); err == nil {
			return int(time.Since(cStart).Milliseconds())
		}
	}

	return -1
}
