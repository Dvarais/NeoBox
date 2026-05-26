package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Settings represents the NeoBox application settings.
type Settings struct {
	TunMode              bool     `json:"tunMode"`
	FakeDns              bool     `json:"fakeDns"`
	Dns                  string   `json:"dns"`
	CustomDirect         []string `json:"customDirect"`
	ProcessMode          string   `json:"processMode"` // "blacklist" or "whitelist"
	ProcessList          []string `json:"processList"`
	ProcessListBlacklist []string `json:"processListBlacklist"`
	ProcessListWhitelist []string `json:"processListWhitelist"`
	BypassRu             bool     `json:"bypassRu"`
	KillSwitch           bool     `json:"killSwitch"`
	// DnsLeak: when true (default), all DNS is routed through VPN to prevent leaks.
	// When false, the local DNS resolver is allowed as fallback.
	DnsLeak              bool     `json:"dnsLeak"`
	// Ipv6Leak: when true (default), IPv6 traffic is rejected to prevent leaks.
	// When false, IPv6 traffic is allowed to bypass the tunnel.
	Ipv6Leak             bool     `json:"ipv6Leak"`
}

// ParseProxyLink parses protocol-specific proxy URLs into a generic outbound map.
func ParseProxyLink(link string) (map[string]interface{}, error) {
	sanitized := strings.TrimSpace(link)
	sanitized = strings.ReplaceAll(sanitized, " ", "%20")
	sanitized = strings.ReplaceAll(sanitized, "\t", "%09")
	u, err := url.Parse(sanitized)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	protocol := strings.ToLower(u.Scheme)
	tag := u.Fragment
	if tag == "" {
		tag = "proxy"
	} else {
		tag, _ = url.QueryUnescape(tag)
	}

	outbound := make(map[string]interface{})
	outbound["type"] = protocol
	outbound["tag"] = tag

	switch protocol {
	case "vless", "trojan":
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		if protocol == "vless" {
			outbound["uuid"] = u.User.Username()
		} else {
			outbound["password"] = u.User.Username()
		}

		params := u.Query()
		transportType := params.Get("type")
		if transportType == "" {
			transportType = "tcp"
		}
		security := params.Get("security")
		if security == "" {
			security = "none"
		}

		if security == "tls" || security == "reality" {
			tlsMap := make(map[string]interface{})
			tlsMap["enabled"] = true
			
			serverName := params.Get("sni")
			if serverName == "" {
				serverName = params.Get("peer")
			}
			if serverName == "" {
				serverName = u.Hostname()
			}
			tlsMap["server_name"] = serverName

			fingerprint := params.Get("fp")
			if fingerprint == "" {
				fingerprint = "chrome"
			}
			tlsMap["utls"] = map[string]interface{}{
				"enabled":     true,
				"fingerprint": fingerprint,
			}

			if transportType == "grpc" {
				tlsMap["alpn"] = []string{"h2"}
			}

			if security == "reality" {
				tlsMap["reality"] = map[string]interface{}{
					"enabled":    true,
					"public_key": params.Get("pbk"),
					"short_id":   params.Get("sid"),
				}
			}
			outbound["tls"] = tlsMap
		}

		if transportType == "ws" {
			wsPath := params.Get("path")
			if wsPath == "" {
				wsPath = "/"
			}
			wsHost := params.Get("host")
			if wsHost == "" {
				wsHost = u.Hostname()
			}
			outbound["transport"] = map[string]interface{}{
				"type": "ws",
				"path": wsPath,
				"headers": map[string]string{
					"Host": wsHost,
				},
			}
		} else if transportType == "grpc" {
			svcName := params.Get("serviceName")
			if svcName == "" {
				svcName = params.Get("servicename")
			}
			if svcName == "" {
				svcName = params.Get("path")
			}
			svcName, _ = url.QueryUnescape(svcName)
			svcName = strings.Trim(svcName, "/")
			outbound["transport"] = map[string]interface{}{
				"type":         "grpc",
				"service_name": svcName,
			}
		}
		return outbound, nil

	case "vmess":
		// VMess usually uses base64-encoded hostname containing a JSON string.
		b64Str := u.Host
		// Pad base64 if needed.
		if len(b64Str)%4 != 0 {
			b64Str += strings.Repeat("=", 4-(len(b64Str)%4))
		}
		decodedBytes, err := base64.StdEncoding.DecodeString(b64Str)
		if err != nil {
			// Try decoding without host/hostname split.
			decodedBytes, err = base64.StdEncoding.DecodeString(u.Hostname())
			if err != nil {
				return nil, fmt.Errorf("failed to decode vmess base64: %w", err)
			}
		}

		var vmessData map[string]interface{}
		if err := json.Unmarshal(decodedBytes, &vmessData); err != nil {
			return nil, fmt.Errorf("failed to parse vmess json: %w", err)
		}

		server, _ := vmessData["add"].(string)
		var portVal int
		switch p := vmessData["port"].(type) {
		case float64:
			portVal = int(p)
		case string:
			portVal, _ = strconv.Atoi(p)
		}
		uuidVal, _ := vmessData["id"].(string)
		psVal, _ := vmessData["ps"].(string)
		if psVal == "" {
			psVal = tag
		}
		if psVal == "" {
			psVal = "proxy"
		}
		outbound["tag"] = psVal

		outbound["type"] = "vmess"
		outbound["server"] = server
		outbound["server_port"] = portVal
		outbound["uuid"] = uuidVal
		outbound["security"] = "auto"

		tlsVal, _ := vmessData["tls"].(string)
		if tlsVal == "tls" {
			sniVal, _ := vmessData["sni"].(string)
			if sniVal == "" {
				sniVal = server
			}
			fpVal, _ := vmessData["fp"].(string)
			if fpVal == "" {
				fpVal = "chrome"
			}
			outbound["tls"] = map[string]interface{}{
				"enabled":     true,
				"server_name": sniVal,
				"utls": map[string]interface{}{
					"enabled":     true,
					"fingerprint": fpVal,
				},
			}
		}

		netVal, _ := vmessData["net"].(string)
		if netVal == "ws" {
			pathVal, _ := vmessData["path"].(string)
			if pathVal == "" {
				pathVal = "/"
			}
			hostVal, _ := vmessData["host"].(string)
			if hostVal == "" {
				hostVal = server
			}
			outbound["transport"] = map[string]interface{}{
				"type": "ws",
				"path": pathVal,
				"headers": map[string]string{
					"Host": hostVal,
				},
			}
		} else if netVal == "grpc" {
			pathVal, _ := vmessData["path"].(string)
			pathVal, _ = url.QueryUnescape(pathVal)
			pathVal = strings.TrimLeft(pathVal, "/")
			outbound["transport"] = map[string]interface{}{
				"type":         "grpc",
				"service_name": pathVal,
			}
		}
		return outbound, nil

	case "ss":
		outbound["type"] = "shadowsocks"
		portInt, _ := strconv.Atoi(u.Port())
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		var authPart string
		if u.User != nil {
			authPart = u.User.String()
		}

		// Shadowsocks legacy base64 parsing.
		if authPart == "" && u.Hostname() != "" && u.Port() == "" {
			b64Str := u.Hostname()
			if len(b64Str)%4 != 0 {
				b64Str += strings.Repeat("=", 4-(len(b64Str)%4))
			}
			decoded, err := base64.StdEncoding.DecodeString(b64Str)
			if err == nil && strings.Contains(string(decoded), "@") {
				parts := strings.SplitN(string(decoded), "@", 2)
				auth := parts[0]
				serverPart := parts[1]

				authParts := strings.SplitN(auth, ":", 2)
				method := authParts[0]
				password := authParts[1]

				serverParts := strings.SplitN(serverPart, ":", 2)
				outbound["server"] = serverParts[0]
				portInt, _ = strconv.Atoi(serverParts[1])
				outbound["server_port"] = portInt
				outbound["method"] = method
				outbound["password"] = password
				return outbound, nil
			}
		}

		if authPart != "" {
			if !strings.Contains(authPart, ":") {
				// Base64 encoded user info.
				if len(authPart)%4 != 0 {
					authPart += strings.Repeat("=", 4-(len(authPart)%4))
				}
				decoded, err := base64.StdEncoding.DecodeString(authPart)
				if err == nil {
					authPart = string(decoded)
				}
			}
			authParts := strings.SplitN(authPart, ":", 2)
			if len(authParts) == 2 {
				outbound["method"] = authParts[0]
				outbound["password"] = authParts[1]
			}
		}
		return outbound, nil

	case "tuic":
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		if u.User != nil {
			outbound["uuid"] = u.User.Username()
			p, _ := u.User.Password()
			outbound["password"] = p
		}

		params := u.Query()
		cc := params.Get("congestion_control")
		if cc == "" {
			cc = "bbr"
		}
		outbound["congestion_control"] = cc

		alpnStr := params.Get("alpn")
		var alpn []string
		if alpnStr != "" {
			alpn = strings.Split(alpnStr, ",")
		} else {
			alpn = []string{"h3"}
		}
		outbound["alpn"] = alpn

		tlsMap := make(map[string]interface{})
		tlsMap["enabled"] = true
		sni := params.Get("sni")
		if sni == "" {
			sni = u.Hostname()
		}
		tlsMap["server_name"] = sni
		outbound["tls"] = tlsMap
		return outbound, nil

	case "hysteria2", "hy2":
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["type"] = "hysteria2"
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		if u.User != nil {
			outbound["password"] = u.User.Username()
		}

		params := u.Query()
		tlsMap := make(map[string]interface{})
		tlsMap["enabled"] = true
		sni := params.Get("sni")
		if sni == "" {
			sni = params.Get("peer")
		}
		if sni == "" {
			sni = u.Hostname()
		}
		tlsMap["server_name"] = sni

		insecure := params.Get("insecure")
		if insecure == "1" || insecure == "true" {
			tlsMap["insecure"] = true
		}
		outbound["tls"] = tlsMap
		return outbound, nil
	}

	return nil, fmt.Errorf("unsupported proxy protocol: %s", protocol)
}

// GenerateConfig generates a complete sing-box configuration map.
func GenerateConfig(outbound map[string]interface{}, settings Settings, useSystemProxy bool, cacheDBPath string) (map[string]interface{}, error) {
	tunMode := settings.TunMode

	// 1. DNS Section (Nuclear Strategy: No local DNS, only IP-based DoH for remote, local for direct)
	dnsServers := []map[string]interface{}{
		{
			"type":   "https",
			"tag":    "dns-remote",
			"server": "1.1.1.1",
			"path":   "/dns-query",
			"detour": outbound["tag"].(string),
			"tls": map[string]interface{}{
				"enabled":     true,
				"server_name": "cloudflare-dns.com",
			},
		},
		{
			"type":   "local",
			"tag":    "dns-direct",
		},
	}

	if settings.FakeDns && tunMode {
		dnsServers = append(dnsServers, map[string]interface{}{
			"tag":         "dns-fake",
			"type":        "fakeip",
			"inet4_range": "198.18.0.0/15",
		})
	}

	// Pre-resolve proxy domain to IP to avoid DNS inside the tunnel.
	// Uses a 2-second timeout so slow DNS won't freeze VPN startup.
	serverDomain, _ := outbound["server"].(string)
	outbound["domain_resolver"] = "dns-direct"
	if net.ParseIP(serverDomain) == nil {
		resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer resolveCancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, serverDomain)
		if err == nil {
			for _, addr := range addrs {
				if addr.IP.To4() != nil {
					outbound["server"] = addr.IP.String()
					break
				}
			}
		}
	}

	// 3. Routing Rules (Aggressive Shielding & Accurate Evaluation Order)
	processMode := settings.ProcessMode
	if processMode == "" {
		processMode = "blacklist"
	}
	var processList []string
	if processMode == "blacklist" {
		if len(settings.ProcessListBlacklist) > 0 {
			processList = settings.ProcessListBlacklist
		} else {
			processList = settings.ProcessList
		}
	} else {
		processList = settings.ProcessListWhitelist
	}

	activeInbounds := []string{"mixed-in"}
	if tunMode {
		activeInbounds = append(activeInbounds, "tun-in")
	}

	routeRules := []map[string]interface{}{
		{"inbound": activeInbounds, "action": "sniff", "timeout": "1s"},
		// DNS hijack must come BEFORE IPv6 reject — otherwise DNS queries over IPv6 get
		// rejected instead of being intercepted, causing potential DNS leaks via IPv6.
		{"port": []int{53}, "action": "hijack-dns"},
		{"protocol": "dns", "action": "hijack-dns"},
	}

	// Block IPv6 to prevent leaks unless the user explicitly disabled Ipv6Leak protection.
	// Default (Ipv6Leak=false in JSON means field is zero-value=false) — treat unset as true
	// for backwards compat: only skip if user has explicitly set ipv6Leak=false.
	if settings.Ipv6Leak {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_version": 6, "action": "reject",
		})
	}

	// Exclude proxy server IP or domain from proxy tunnel
	serverIPStr, _ := outbound["server"].(string)
	if net.ParseIP(serverIPStr) != nil {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_cidr":  []string{serverIPStr + "/32"},
			"action":   "route",
			"outbound": "direct",
		})
	} else {
		routeRules = append(routeRules, map[string]interface{}{
			"domain":   []string{serverIPStr},
			"action":   "route",
			"outbound": "direct",
		})
	}

	// Custom direct domains
	var validDirect []string
	for _, domain := range settings.CustomDirect {
		d := strings.TrimSpace(domain)
		if d != "" {
			validDirect = append(validDirect, d)
		}
	}
	if len(validDirect) > 0 {
		routeRules = append(routeRules, map[string]interface{}{
			"domain_suffix": validDirect,
			"action":        "route",
			"outbound":      "direct",
		})
	}

	// Process routing (split tunneling)
	if tunMode && len(processList) > 0 {
		if processMode == "blacklist" {
			routeRules = append(routeRules, map[string]interface{}{
				"process_name": processList,
				"action":       "route",
				"outbound":     "direct",
			})
		} else {
			routeRules = append(routeRules, map[string]interface{}{
				"process_name": processList,
				"action":       "route",
				"outbound":     outbound["tag"].(string),
			})
			routeRules = append(routeRules, map[string]interface{}{
				"action":   "route",
				"outbound": "direct",
			})
		}
	}

	// Bypass Russia (rule-sets)
	if settings.BypassRu {
		routeRules = append(routeRules, map[string]interface{}{
			"rule_set": []string{"geoip-ru", "geosite-ru"},
			"action":   "route",
			"outbound": "direct",
		})
	}

	// Local and private IPs
	routeRules = append(routeRules, map[string]interface{}{
		"ip_is_private": true,
		"action":        "route",
		"outbound":      "direct",
	})

	// FakeIP Cidr rule (must be placed AFTER domain and process exclusions!)
	if settings.FakeDns && tunMode {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_cidr":  []string{"198.18.0.0/15"},
			"action":   "route",
			"outbound": outbound["tag"].(string),
		})
	}

	// 4. Final Config Structure
	dnsRules := []map[string]interface{}{
		{
			"domain":        []string{serverIPStr, "localhost", "wails.localhost"},
			"domain_suffix": []string{".local", ".localhost"},
			"server":        "dns-direct",
		},
	}
	if settings.FakeDns && tunMode {
		dnsRules = append(dnsRules, map[string]interface{}{
			"query_type": []string{"A", "AAAA"},
			"action":     "route",
			"server":     "dns-fake",
		})
	}

	config := map[string]interface{}{
		"log": map[string]interface{}{
			"level":     "info",
			"timestamp": true,
		},
		"dns": map[string]interface{}{
			"servers":         dnsServers,
			"rules":           dnsRules,
			"final":           "dns-remote",
			"strategy":        "ipv4_only",
			"reverse_mapping": true,
		},
		"inbounds": []map[string]interface{}{
			{
				"type":             "mixed",
				"tag":              "mixed-in",
				"listen":           "127.0.0.1",
				"listen_port":      20809,
				"set_system_proxy": useSystemProxy,
			},
		},
		"outbounds": []interface{}{
			outbound,
			map[string]interface{}{"type": "direct", "tag": "direct"},
			map[string]interface{}{"type": "block", "tag": "block"},
		},
		"route": map[string]interface{}{
			"rules":                   routeRules,
			"auto_detect_interface":   true,
			"default_domain_resolver": "dns-direct",
			"final":                   outbound["tag"].(string),
		},
		"experimental": map[string]interface{}{
			"cache_file": map[string]interface{}{
				"enabled":       true,
				"path":          cacheDBPath,
				"store_fakeip":  true,
			},
			"clash_api": map[string]interface{}{
				"external_controller": "127.0.0.1:9097",
			},
		},
	}

	if settings.BypassRu {
		routeSection := config["route"].(map[string]interface{})
		routeSection["rule_set"] = []map[string]interface{}{
			{
				"tag":             "geoip-ru",
				"type":            "remote",
				"format":          "binary",
				"url":             "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-ru.srs",
				"download_detour": "direct",
			},
			{
				"tag":             "geosite-ru",
				"type":            "remote",
				"format":          "binary",
				"url":             "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-ru.srs",
				"download_detour": "direct",
			},
		}
	}

	if tunMode {
		inboundsSection := config["inbounds"].([]map[string]interface{})
		inboundsSection = append(inboundsSection, map[string]interface{}{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "tun-neobox",
			"address":        []string{"172.18.0.1/30", "fdfe:dcba:9876::1/126"},
			"auto_route":      true,
			"strict_route":    true,
			"stack":          "gvisor",
			"mtu":            1280,
		})
		config["inbounds"] = inboundsSection
	}

	return config, nil
}

// FetchSubscription loads subscription contents (both JSON-based xray-ext and standard lists).
func FetchSubscription(subURL string) ([]string, error) {
	trimmedURL := strings.TrimSpace(subURL)
	trimmedURL = strings.ReplaceAll(trimmedURL, " ", "%20")
	trimmedURL = strings.ReplaceAll(trimmedURL, "\t", "%09")
	lowerURL := strings.ToLower(trimmedURL)
	if strings.HasPrefix(lowerURL, "vless://") || strings.HasPrefix(lowerURL, "vmess://") ||
		strings.HasPrefix(lowerURL, "ss://") || strings.HasPrefix(lowerURL, "trojan://") ||
		strings.HasPrefix(lowerURL, "tuic://") || strings.HasPrefix(lowerURL, "hysteria2://") ||
		strings.HasPrefix(lowerURL, "hy2://") {
		return []string{trimmedURL}, nil
	}

	var rawData string
	var bodyBytes []byte
	var fetchErr error

	// 1. Try local proxy first (highly likely to succeed if VPN is connected)
	proxyURL, proxyErr := url.Parse("http://127.0.0.1:20809")
	if proxyErr == nil {
		req, reqErr := http.NewRequest("GET", trimmedURL, nil)
		if reqErr == nil {
			req.Header.Set("User-Agent", "v2rayN")
			proxyClient := &http.Client{
				Timeout: 10 * time.Second,
				Transport: &http.Transport{
					Proxy: http.ProxyURL(proxyURL),
				},
			}
			resp, err := proxyClient.Do(req)
			if err == nil {
				limitReader := io.LimitReader(resp.Body, 20*1024*1024)
				tempBytes, _ := io.ReadAll(limitReader)
				resp.Body.Close()
				
				tempData := strings.TrimSpace(string(tempBytes))
				isHTML := strings.Contains(strings.ToLower(tempData), "<html") || strings.Contains(strings.ToLower(tempData), "<script")
				if !isHTML && len(tempData) > 0 {
					rawData = tempData
					bodyBytes = tempBytes
				} else {
					fetchErr = fmt.Errorf("proxy returned HTML or empty data")
				}
			} else {
				fetchErr = err
			}
		}
	}

	// 2. Fallback/Try direct fetch if proxy failed, returned HTML, or is not running
	if rawData == "" {
		req, reqErr := http.NewRequest("GET", trimmedURL, nil)
		if reqErr == nil {
			req.Header.Set("User-Agent", "v2rayN")
			directClient := &http.Client{
				Timeout: 15 * time.Second,
			}
			resp, err := directClient.Do(req)
			if err == nil {
				limitReader := io.LimitReader(resp.Body, 20*1024*1024)
				tempBytes, _ := io.ReadAll(limitReader)
				resp.Body.Close()
				
				tempData := strings.TrimSpace(string(tempBytes))
				isHTML := strings.Contains(strings.ToLower(tempData), "<html") || strings.Contains(strings.ToLower(tempData), "<script")
				if !isHTML && len(tempData) > 0 {
					rawData = tempData
					bodyBytes = tempBytes
				} else {
					if fetchErr != nil {
						fetchErr = fmt.Errorf("direct returned HTML or empty; proxy error: %v", fetchErr)
					} else {
						fetchErr = fmt.Errorf("direct returned HTML or empty data")
					}
				}
			} else {
				if fetchErr != nil {
					fetchErr = fmt.Errorf("direct error: %v; proxy error: %v", err, fetchErr)
				} else {
					fetchErr = err
				}
			}
		}
	}

	if rawData == "" {
		return nil, fmt.Errorf("failed to fetch subscription: %v", fetchErr)
	}

	// Try parsing xray-ext JSON subscription format
	if strings.HasPrefix(rawData, "[") && strings.HasSuffix(rawData, "]") {
		var jsonArray []map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &jsonArray); err == nil {
			var links []string
			for _, item := range jsonArray {
				outbounds, _ := item["outbounds"].([]interface{})
				remarks, _ := item["remarks"].(string)

				for _, outboundVal := range outbounds {
					outbound, ok := outboundVal.(map[string]interface{})
					if !ok {
						continue
					}
					proto, _ := outbound["protocol"].(string)
					tag, _ := outbound["tag"].(string)

					if remarks == "" {
						remarks = tag
					}
					if remarks == "" {
						remarks = "Proxy"
					}

					// Skip auto-select placeholder node
					if strings.Contains(remarks, "Автовыбор") && tag == "auto" {
						continue
					}

					switch proto {
					case "vless":
						settings, _ := outbound["settings"].(map[string]interface{})
						vnexts, _ := settings["vnext"].([]interface{})
						if len(vnexts) > 0 {
							vnext, _ := vnexts[0].(map[string]interface{})
							address, _ := vnext["address"].(string)
							var portVal int
							switch p := vnext["port"].(type) {
							case float64:
								portVal = int(p)
							}
							users, _ := vnext["users"].([]interface{})
							if len(users) > 0 {
								user, _ := users[0].(map[string]interface{})
								idVal, _ := user["id"].(string)
								flowVal, _ := user["flow"].(string)

								stream, _ := outbound["streamSettings"].(map[string]interface{})
								network, _ := stream["network"].(string)
								if network == "" {
									network = "tcp"
								}
								security, _ := stream["security"].(string)
								if security == "" {
									security = "none"
								}

								var queryParams []string
								if security == "reality" {
									reality, _ := stream["realitySettings"].(map[string]interface{})
									if reality != nil {
										if sni, _ := reality["serverName"].(string); sni != "" {
											queryParams = append(queryParams, "sni="+url.QueryEscape(sni))
										}
										if fp, _ := reality["fingerprint"].(string); fp != "" {
											queryParams = append(queryParams, "fp="+url.QueryEscape(fp))
										}
										if pbk, _ := reality["publicKey"].(string); pbk != "" {
											queryParams = append(queryParams, "pbk="+url.QueryEscape(pbk))
										}
										if sid, _ := reality["shortId"].(string); sid != "" {
											queryParams = append(queryParams, "sid="+url.QueryEscape(sid))
										}
									}
								} else if security == "tls" {
									tls, _ := stream["tlsSettings"].(map[string]interface{})
									if tls != nil {
										if sni, _ := tls["serverName"].(string); sni != "" {
											queryParams = append(queryParams, "sni="+url.QueryEscape(sni))
										}
									}
								}

								if network == "ws" {
									ws, _ := stream["wsSettings"].(map[string]interface{})
									if ws != nil {
										queryParams = append(queryParams, "type=ws")
										if path, _ := ws["path"].(string); path != "" {
											queryParams = append(queryParams, "path="+url.QueryEscape(path))
										}
										headers, _ := ws["headers"].(map[string]interface{})
										if headers != nil {
											if host, _ := headers["Host"].(string); host != "" {
												queryParams = append(queryParams, "host="+url.QueryEscape(host))
											}
										}
									}
								} else {
									queryParams = append(queryParams, "type="+network)
								}

								if flowVal != "" {
									queryParams = append(queryParams, "flow="+flowVal)
								}
								if security != "" {
									queryParams = append(queryParams, "security="+security)
								}

								queryString := ""
								if len(queryParams) > 0 {
									queryString = "?" + strings.Join(queryParams, "&")
								}
								links = append(links, fmt.Sprintf("vless://%s@%s:%d%s#%s", idVal, address, portVal, queryString, url.QueryEscape(remarks)))
							}
						}

					case "trojan":
						settings, _ := outbound["settings"].(map[string]interface{})
						servers, _ := settings["servers"].([]interface{})
						if len(servers) > 0 {
							server, _ := servers[0].(map[string]interface{})
							address, _ := server["address"].(string)
							var portVal int
							switch p := server["port"].(type) {
							case float64:
								portVal = int(p)
							}
							passwordVal, _ := server["password"].(string)

							stream, _ := outbound["streamSettings"].(map[string]interface{})
							network, _ := stream["network"].(string)
							if network == "" {
								network = "tcp"
							}
							security, _ := stream["security"].(string)
							if security == "" {
								security = "none"
							}

							var queryParams []string
							if security == "tls" {
								tls, _ := stream["tlsSettings"].(map[string]interface{})
								if tls != nil {
									if sni, _ := tls["serverName"].(string); sni != "" {
										queryParams = append(queryParams, "sni="+url.QueryEscape(sni))
									}
								}
							}

							if network == "ws" {
								ws, _ := stream["wsSettings"].(map[string]interface{})
								if ws != nil {
									queryParams = append(queryParams, "type=ws")
									if path, _ := ws["path"].(string); path != "" {
										queryParams = append(queryParams, "path="+url.QueryEscape(path))
									}
									headers, _ := ws["headers"].(map[string]interface{})
									if headers != nil {
										if host, _ := headers["Host"].(string); host != "" {
											queryParams = append(queryParams, "host="+url.QueryEscape(host))
										}
									}
								}
							} else {
								queryParams = append(queryParams, "type="+network)
							}

							if security != "" {
								queryParams = append(queryParams, "security="+security)
							}

							queryString := ""
							if len(queryParams) > 0 {
								queryString = "?" + strings.Join(queryParams, "&")
							}
							links = append(links, fmt.Sprintf("trojan://%s@%s:%d%s#%s", passwordVal, address, portVal, queryString, url.QueryEscape(remarks)))
						}
					}
				}
			}
			if len(links) > 0 {
				return links, nil
			}
		}
	}

	// Otherwise parse as standard newline separated plain text list (sometimes base64 encoded)
	// First, check if the entire payload is a single-line or multi-line base64 block
	cleanRawData := strings.ReplaceAll(rawData, "\r", "")
	cleanRawData = strings.ReplaceAll(cleanRawData, "\n", "")
	cleanRawData = strings.ReplaceAll(cleanRawData, " ", "")
	cleanRawData = strings.ReplaceAll(cleanRawData, "\t", "")

	if !strings.Contains(cleanRawData, "://") && cleanRawData != "" {
		padded := cleanRawData
		if len(padded)%4 != 0 {
			padded += strings.Repeat("=", 4-(len(padded)%4))
		}
		
		decodedBytes, err := base64.StdEncoding.DecodeString(padded)
		if err == nil {
			rawData = string(decodedBytes)
		} else {
			decodedBytes, err = base64.URLEncoding.DecodeString(padded)
			if err == nil {
				rawData = string(decodedBytes)
			}
		}
	}

	lines := strings.Split(rawData, "\n")
	var parsedLinks []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		
		// If it is already a plain text link
		trimmedLower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmedLower, "vless://") || strings.HasPrefix(trimmedLower, "vmess://") ||
			strings.HasPrefix(trimmedLower, "ss://") || strings.HasPrefix(trimmedLower, "trojan://") ||
			strings.HasPrefix(trimmedLower, "tuic://") || strings.HasPrefix(trimmedLower, "hysteria2://") ||
			strings.HasPrefix(trimmedLower, "hy2://") {
			parsedLinks = append(parsedLinks, trimmed)
			continue
		}
		
		// Otherwise, try to decode this individual line from base64 (MIME/line-by-line format)
		padded := trimmed
		if len(padded)%4 != 0 {
			padded += strings.Repeat("=", 4-(len(padded)%4))
		}
		
		// Try standard base64 decoding
		decodedBytes, err := base64.StdEncoding.DecodeString(padded)
		if err == nil {
			decodedStr := strings.TrimSpace(string(decodedBytes))
			if strings.Contains(decodedStr, "://") {
				parsedLinks = append(parsedLinks, decodedStr)
				continue
			}
		}
		
		// Try URL-safe base64 decoding
		decodedBytes, err = base64.URLEncoding.DecodeString(padded)
		if err == nil {
			decodedStr := strings.TrimSpace(string(decodedBytes))
			if strings.Contains(decodedStr, "://") {
				parsedLinks = append(parsedLinks, decodedStr)
				continue
			}
		}
	}

	return parsedLinks, nil
}
