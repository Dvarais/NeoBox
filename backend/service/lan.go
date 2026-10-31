package service

import (
	"encoding/base64"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/skip2/go-qrcode"
)

// LanIPInfo represents a local network interface and its assigned IPv4 address.
type LanIPInfo struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// isPrivateIPv4 checks whether an IPv4 belongs to standard private subnets (RFC 1918):
// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
func isPrivateIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 10 ||
		(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
		(ip4[0] == 192 && ip4[1] == 168)
}

// GetLanIPs scans active network interfaces and returns non-loopback IPv4 addresses
// suitable for sharing the proxy in a local network.
func (s *AppService) GetLanIPs() ([]LanIPInfo, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var results []LanIPInfo
	seen := make(map[string]bool)

	// First pass: look for private IPv4 addresses
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}

			ipStr := ip4.String()
			if isPrivateIPv4(ip4) && !seen[ipStr] {
				seen[ipStr] = true
				results = append(results, LanIPInfo{
					Name: strings.TrimSpace(iface.Name),
					IP:   ipStr,
				})
			}
		}
	}

	// Fallback pass: if no RFC 1918 addresses were found, include any non-loopback IPv4
	if len(results) == 0 {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok {
					if ip4 := ipNet.IP.To4(); ip4 != nil && !ipNet.IP.IsLoopback() && !seen[ip4.String()] {
						seen[ip4.String()] = true
						results = append(results, LanIPInfo{
							Name: strings.TrimSpace(iface.Name),
							IP:   ip4.String(),
						})
					}
				}
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].IP < results[j].IP
	})

	return results, nil
}

// GenerateQRCodePNG encodes the provided text into a base64 PNG Data URI.
func (s *AppService) GenerateQRCodePNG(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", fmt.Errorf("empty content for QR code")
	}
	pngBytes, err := qrcode.Encode(trimmed, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes), nil
}
