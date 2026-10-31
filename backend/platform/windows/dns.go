//go:build windows

package windows

import (
	"golang.org/x/sys/windows"
)

var (
	modDnsapi                 = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsFlushResolverCache = modDnsapi.NewProc("DnsFlushResolverCache")
)

type DNSManager struct{}

// FlushCache flushes the Windows local DNS resolver cache using DnsFlushResolverCache from dnsapi.dll.
func (d *DNSManager) FlushCache() error {
	r1, _, err := procDnsFlushResolverCache.Call()
	if r1 == 0 {
		return err
	}
	return nil
}
