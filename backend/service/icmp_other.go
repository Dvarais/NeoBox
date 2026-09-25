//go:build !windows

package service

import (
	"time"

	"NeoBox/backend/platform"
)

func icmpEchoLatency(host string, timeout time.Duration) int {
	if p := platform.Current(); p != nil && p.Prober() != nil {
		return p.Prober().Ping(host, timeout)
	}
	return -1
}
