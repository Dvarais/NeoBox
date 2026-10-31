//go:build linux

package linux

type DNSManager struct{}

// FlushCache flushes the local DNS resolver cache on Linux (no-op fallback).
func (d *DNSManager) FlushCache() error {
	return nil
}
