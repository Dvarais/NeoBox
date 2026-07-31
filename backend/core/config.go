package core

import (
	"NeoBox/backend/i18n"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// realisticUserAgents is a pool of browser-like User-Agent strings. Sending a
// recognisable client name ("v2rayN" and friends) identifies the traffic as a
// VPN tool to the subscription host and to anyone observing the request.
var realisticUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 Edg/125.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:127.0) Gecko/20100101 Firefox/127.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 OPR/112.0.0.0",
}

// subscriptionClientUA names NeoBox as a proxy client. Some subscription hosts
// sniff the User-Agent and serve a human-facing HTML activation page to
// anything that looks like a browser, handing the node list only to a
// recognised client. This is sent solely as a retry after a browser User-Agent
// has been turned away, so an ordinary fetch still keeps NeoBox anonymous.
const subscriptionClientUA = "v2rayN/6.45"

// getRandomUserAgent returns a random browser User-Agent string from the pool.
func getRandomUserAgent() string {
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(realisticUserAgents))))
	if err != nil {
		// Fallback to first entry if crypto/rand fails (extremely unlikely)
		return realisticUserAgents[0]
	}
	return realisticUserAgents[idx.Int64()]
}

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
	// DnsLeakProtection: when true (default), all DNS is routed through VPN to prevent leaks.
	// When false, the local DNS resolver is allowed as fallback.
	// Pointer so we can distinguish absent JSON key (nil → default true) from explicit false.
	DnsLeakProtection *bool `json:"dnsLeak"`
	// Ipv6LeakProtection: when true (default), IPv6 traffic is rejected to prevent leaks.
	// When false, IPv6 traffic is allowed to bypass the tunnel.
	// Pointer so we can distinguish absent JSON key (nil → default true) from explicit false.
	Ipv6LeakProtection *bool `json:"ipv6Leak"`
	// CustomRules are user-defined routing rules added before geoip/geosite rules.
	CustomRules []CustomRule `json:"customRules"`
	// VerboseLogging raises the core log level from "warn" to "info". At "info"
	// sing-box logs the open and the close of every connection, which is useful
	// when diagnosing routing but is a firehose during ordinary browsing — and
	// every line has to cross into the WebView2 renderer. Off by default.
	VerboseLogging bool `json:"verboseLogging"`
}

// CustomRule represents a single user-defined routing rule.
type CustomRule struct {
	Action string `json:"action"` // "direct", "block", "proxy"
	Type   string `json:"type"`   // "domain", "domain_suffix", "domain_keyword", "ip_cidr"
	Value  string `json:"value"`
}

// boolDefault returns the dereferenced value of b, or def if b is nil.
// Used to implement "true by default" for optional bool settings.
func boolDefault(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// decodeBase64Any decodes a base64 payload written in any of the four common
// spellings: the standard or the URL-safe alphabet, padded or not. Clients
// disagree on which they emit and a subscription is not worth losing over it.
func decodeBase64Any(payload string) ([]byte, error) {
	padded := strings.TrimSpace(payload)
	if len(padded)%4 != 0 {
		padded += strings.Repeat("=", 4-(len(padded)%4))
	}

	decoded, err := base64.StdEncoding.DecodeString(padded)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(padded)
}

// unescapeOrKeep percent-decodes a URL component, leaving it untouched when it
// is not valid escaping — a literal "%" in a password is likelier than an
// intentional escape, and dropping the value would be worse than either.
func unescapeOrKeep(component string) string {
	if unescaped, err := url.PathUnescape(component); err == nil {
		return unescaped
	}
	return component
}

// copyFirstWireGuardPeer replaces the "peers" list of a WireGuard outbound with
// a copy and returns the first peer, ready to be modified. It returns nil for
// anything that is not a WireGuard endpoint with at least one peer.
//
// The copy exists because GenerateConfig promises not to touch the caller's
// outbound: its own copy is shallow, so the peer maps underneath are still the
// caller's until they are duplicated here.
func copyFirstWireGuardPeer(outbound map[string]interface{}) map[string]interface{} {
	if outboundType, _ := outbound["type"].(string); outboundType != "wireguard" {
		return nil
	}
	peers, _ := outbound["peers"].([]interface{})
	if len(peers) == 0 {
		return nil
	}

	copied := make([]interface{}, 0, len(peers))
	var first map[string]interface{}
	for _, item := range peers {
		peer, ok := item.(map[string]interface{})
		if !ok {
			copied = append(copied, item)
			continue
		}
		clone := make(map[string]interface{}, len(peer))
		for k, v := range peer {
			clone[k] = v
		}
		if first == nil {
			first = clone
		}
		copied = append(copied, clone)
	}
	outbound["peers"] = copied
	return first
}

// withPrefixLength gives a WireGuard interface address the prefix length
// sing-box requires. Clash and most share links carry a bare address, meaning
// "this address only", which is /32 for IPv4 and /128 for IPv6.
func withPrefixLength(address string) string {
	if strings.Contains(address, "/") {
		return address
	}
	if strings.Contains(address, ":") {
		return address + "/128"
	}
	return address + "/32"
}

// parseReserved reads the three reserved bytes WARP-style WireGuard peers
// prepend to each packet. They are written either as decimals ("0,0,0") or as
// the base64 of the same three bytes; anything else yields nil, leaving the
// field out rather than sending a wrong header.
func parseReserved(value string) []int {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if parts := strings.Split(value, ","); len(parts) == 3 {
		reserved := make([]int, 0, 3)
		for _, part := range parts {
			b, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || b < 0 || b > 255 {
				return nil
			}
			reserved = append(reserved, b)
		}
		return reserved
	}

	if decoded, err := decodeBase64Any(value); err == nil && len(decoded) == 3 {
		return []int{int(decoded[0]), int(decoded[1]), int(decoded[2])}
	}
	return nil
}

// ParseProxyLink parses protocol-specific proxy URLs into a generic outbound map.
// Links longer than maxLinkLength are rejected before any parsing, so a
// malformed or hostile entry cannot turn into unbounded work.
func ParseProxyLink(link string) (map[string]interface{}, error) {
	sanitized := strings.TrimSpace(link)

	if len(sanitized) > maxLinkLength {
		return nil, errors.New(i18n.T(i18n.ErrLinkTooLong, maxLinkLength))
	}

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

		// VLESS flow control (e.g. xtls-rprx-vision). Required by VLESS+Reality
		// and XTLS-Vision servers — without it the connection hangs/refused
		// because most Reality servers only accept the vision flow.
		// Flow is only valid for VLESS over raw TCP or gRPC with TLS/Reality.
		if protocol == "vless" {
			flow := params.Get("flow")
			if flow != "" {
				outbound["flow"] = flow
			}
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

			if transportType == "grpc" || transportType == "http" || transportType == "h2" || transportType == "xhttp" || transportType == "splithttp" {
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
		} else if transportType == "httpupgrade" {
			hupPath := params.Get("path")
			if hupPath == "" {
				hupPath = "/"
			}
			hupHost := params.Get("host")
			if hupHost == "" {
				hupHost = u.Hostname()
			}
			outbound["transport"] = map[string]interface{}{
				"type": "httpupgrade",
				"host": hupHost,
				"path": hupPath,
			}
		} else if transportType == "xhttp" || transportType == "splithttp" {
			// XHTTP/SplitHTTP is an Xray-core transport, and sing-box 1.13 has
			// no implementation of it — the transport list is tcp, ws, quic,
			// grpc and httpupgrade. NeoBox used to map it onto HTTP/2 as "the
			// closest alternative", but the two are not wire-compatible: the
			// node then failed at connect time with an error about the server,
			// which sent people looking in the wrong place. Say what is
			// actually wrong instead.
			return nil, errors.New(i18n.T(i18n.ErrTransportUnsupported, transportType))
		} else if transportType == "http" || transportType == "h2" {
			httpPath := params.Get("path")
			if httpPath == "" {
				httpPath = "/"
			}
			httpHost := params.Get("host")
			if httpHost == "" {
				httpHost = u.Hostname()
			}
			httpMethod := params.Get("method")
			if httpMethod == "" {
				httpMethod = "GET"
			}
			outbound["transport"] = map[string]interface{}{
				"type":   "http",
				"host":   []string{httpHost},
				"path":   httpPath,
				"method": httpMethod,
			}
		}
		return outbound, nil

	case "vmess":
		// VMess carries a base64-encoded JSON object where a host would be.
		//
		// The payload is taken from the raw string rather than u.Host: standard
		// base64 can contain "/", which url.Parse reads as the start of a path
		// and so would cut the payload in half. The alphabet is not fixed
		// either — clients emit both standard and URL-safe base64 — so each is
		// tried in turn.
		b64Str := sanitized[strings.Index(sanitized, "://")+len("://"):]
		if i := strings.Index(b64Str, "#"); i >= 0 {
			b64Str = b64Str[:i]
		}
		b64Str = strings.TrimSuffix(b64Str, "/")
		decodedBytes, err := decodeBase64Any(b64Str)
		if err != nil {
			return nil, fmt.Errorf("failed to decode vmess base64: %w", err)
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
		} else if netVal == "httpupgrade" {
			pathVal, _ := vmessData["path"].(string)
			if pathVal == "" {
				pathVal = "/"
			}
			hostVal, _ := vmessData["host"].(string)
			if hostVal == "" {
				hostVal = server
			}
			outbound["transport"] = map[string]interface{}{
				"type": "httpupgrade",
				"host": hostVal,
				"path": pathVal,
			}
		} else if netVal == "xhttp" || netVal == "splithttp" {
			// Not a transport sing-box has — see the same case for VLESS above.
			return nil, errors.New(i18n.T(i18n.ErrTransportUnsupported, netVal))
		} else if netVal == "http" || netVal == "h2" {
			pathVal, _ := vmessData["path"].(string)
			if pathVal == "" {
				pathVal = "/"
			}
			hostVal, _ := vmessData["host"].(string)
			if hostVal == "" {
				hostVal = server
			}
			methodVal, _ := vmessData["method"].(string)
			if methodVal == "" {
				methodVal = "GET"
			}
			outbound["transport"] = map[string]interface{}{
				"type":   "http",
				"host":   []string{hostVal},
				"path":   pathVal,
				"method": methodVal,
			}
		}
		return outbound, nil

	case "ss":
		outbound["type"] = "shadowsocks"
		portInt, _ := strconv.Atoi(u.Port())
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		// SIP003 plugin, e.g. "?plugin=obfs-local;obfs=http;obfs-host=cdn.com".
		// Set before the branches below, each of which returns on its own.
		// Dropping it — as NeoBox used to — leaves a node that imports cleanly
		// and then cannot connect, because the server expects the obfuscation.
		if name, opts := parsePluginParam(u.Query().Get("plugin")); name != "" {
			outbound["plugin"] = name
			if opts != "" {
				outbound["plugin_opts"] = opts
			}
		}

		var authPart string
		if u.User != nil {
			authPart = u.User.String()
		}

		// Shadowsocks legacy base64 parsing.
		if authPart == "" && u.Hostname() != "" && u.Port() == "" {
			decoded, err := decodeBase64Any(u.Hostname())
			if err == nil && strings.Contains(string(decoded), "@") {
				parts := strings.SplitN(string(decoded), "@", 2)
				auth := parts[0]
				serverPart := parts[1]

				authParts := strings.SplitN(auth, ":", 2)
				// SplitN returns a 1-element slice when the separator is absent, so
				// indexing [1] without this check panics on a malformed link.
				if len(authParts) < 2 {
					return nil, fmt.Errorf("invalid shadowsocks legacy auth format: missing ':'")
				}
				method := authParts[0]
				password := authParts[1]

				serverParts := strings.SplitN(serverPart, ":", 2)
				// Same hazard as the auth part above: no ":" means no index 1.
				if len(serverParts) < 2 {
					return nil, fmt.Errorf("invalid shadowsocks legacy server format: missing port in %q", serverPart)
				}
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
				if decoded, err := decodeBase64Any(authPart); err == nil {
					authPart = string(decoded)
				}
			}
			authParts := strings.SplitN(authPart, ":", 2)
			if len(authParts) == 2 {
				// u.User.String() hands back the userinfo still percent-escaped,
				// so a password containing "@", "/" or a space arrives encoded.
				// PathUnescape and not QueryUnescape: the latter would turn a
				// "+" inside a password into a space.
				outbound["method"] = unescapeOrKeep(authParts[0])
				outbound["password"] = unescapeOrKeep(authParts[1])
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

		// Obfuscation (Salamander). Format in share links:
		//   obfs=salamander&obfs-password=SECRET
		obfsType := params.Get("obfs")
		if obfsType != "" {
			outbound["obfs"] = map[string]interface{}{
				"type":     obfsType,
				"password": params.Get("obfs-password"),
			}
		}

		// Bandwidth hints (optional). Hysteria2 share links use upmbps/downmbps.
		if up := params.Get("upmbps"); up != "" {
			if v, err := strconv.Atoi(up); err == nil {
				outbound["up_mbps"] = v
			}
		}
		if down := params.Get("downmbps"); down != "" {
			if v, err := strconv.Atoi(down); err == nil {
				outbound["down_mbps"] = v
			}
		}

		// Optional port hopping range, e.g. "mport=20000-40000".
		if mport := params.Get("mport"); mport != "" {
			outbound["server_ports"] = mport
		}

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

		// ALPN. Hysteria2 runs over HTTP/3 (QUIC); default hy2 if not provided.
		alpnStr := params.Get("alpn")
		if alpnStr != "" {
			tlsMap["alpn"] = strings.Split(alpnStr, ",")
		} else {
			tlsMap["alpn"] = []string{"hy2"}
		}

		insecure := params.Get("insecure")
		if insecure == "1" || insecure == "true" {
			tlsMap["insecure"] = true
		}
		outbound["tls"] = tlsMap
		return outbound, nil

	case "hysteria":
		// Hysteria v1 (QUIC-based). Share link format:
		//   hysteria://host:port?protocol=udp&auth=SECRET&peer=sni&insecure=1&up=50&down=200&alpn=h3#name
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["type"] = "hysteria"
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		params := u.Query()

		// Authentication. Hysteria v1 uses auth_str (string) by convention.
		auth := params.Get("auth")
		if auth == "" {
			auth = params.Get("authStr")
		}
		if auth != "" {
			outbound["auth_str"] = auth
		}

		// Obfuscation string (salamander-style, v1).
		if obfs := params.Get("obfsParam"); obfs != "" {
			outbound["obfs"] = obfs
		}

		// Bandwidth. v1 uses "up"/"down" as e.g. "50 Mbps" or "100 Mbps".
		if up := params.Get("up"); up != "" {
			outbound["up"] = up
		}
		if down := params.Get("down"); down != "" {
			outbound["down"] = down
		}

		// Optional port hopping range, e.g. "mport=20000-40000".
		if mport := params.Get("mport"); mport != "" {
			outbound["server_ports"] = mport
		}

		tlsMap := make(map[string]interface{})
		tlsMap["enabled"] = true
		sni := params.Get("peer")
		if sni == "" {
			sni = params.Get("sni")
		}
		if sni == "" {
			sni = u.Hostname()
		}
		tlsMap["server_name"] = sni

		alpnStr := params.Get("alpn")
		if alpnStr != "" {
			tlsMap["alpn"] = strings.Split(alpnStr, ",")
		} else {
			tlsMap["alpn"] = []string{"h3"}
		}

		insecure := params.Get("insecure")
		if insecure == "1" || insecure == "true" {
			tlsMap["insecure"] = true
		}
		outbound["tls"] = tlsMap
		return outbound, nil

	case "anytls":
		// AnyTLS share link: anytls://PASSWORD@host:port?sni=…&insecure=1#name
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		if u.User != nil {
			// sing-box authenticates with a single password. Links usually carry
			// it alone, but a "user:password@" pair also occurs in the wild —
			// there the second half is the secret.
			password := u.User.Username()
			if p, ok := u.User.Password(); ok && p != "" {
				password = p
			}
			outbound["password"] = password
		}

		params := u.Query()

		// AnyTLS is TLS-only by definition, so the block is unconditional.
		tlsMap := make(map[string]interface{})
		tlsMap["enabled"] = true
		sni := params.Get("sni")
		if sni == "" {
			sni = u.Hostname()
		}
		tlsMap["server_name"] = sni
		if fp := params.Get("fp"); fp != "" {
			tlsMap["utls"] = map[string]interface{}{
				"enabled":     true,
				"fingerprint": fp,
			}
		}
		if alpnStr := params.Get("alpn"); alpnStr != "" {
			tlsMap["alpn"] = strings.Split(alpnStr, ",")
		}
		if insecure := params.Get("insecure"); insecure == "1" || insecure == "true" {
			tlsMap["insecure"] = true
		}
		outbound["tls"] = tlsMap
		return outbound, nil

	case "socks", "socks5":
		// Both spellings are the same outbound; sing-box knows it as "socks".
		outbound["type"] = "socks"
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 1080
		}
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt
		outbound["version"] = "5"

		if u.User != nil {
			if username := u.User.Username(); username != "" {
				outbound["username"] = username
			}
			if password, ok := u.User.Password(); ok && password != "" {
				outbound["password"] = password
			}
		}
		return outbound, nil

	case "wireguard":
		// wireguard://PRIVATE_KEY@host:port?publickey=…&address=172.16.0.2/32&…
		// which is the shape v2rayN and NekoBox write. The private key is
		// base64 and can contain "/", so it travels percent-escaped in the
		// userinfo and url.Parse hands it back decoded.
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 51820
		}
		params := u.Query()

		// Unlike every other protocol here, WireGuard needs the address of the
		// local tunnel interface: it is part of the key exchange, not something
		// the peer can hand out afterwards.
		var addresses []string
		for _, address := range strings.Split(params.Get("address"), ",") {
			if address = strings.TrimSpace(address); address != "" {
				addresses = append(addresses, withPrefixLength(address))
			}
		}
		if len(addresses) == 0 {
			return nil, errors.New(i18n.T(i18n.ErrWireGuardNoAddress))
		}

		peer := map[string]interface{}{
			"address":     u.Hostname(),
			"port":        portInt,
			"public_key":  params.Get("publickey"),
			"allowed_ips": []string{"0.0.0.0/0", "::/0"},
		}
		if psk := params.Get("presharedkey"); psk != "" {
			peer["pre_shared_key"] = psk
		}
		if keepalive, err := strconv.Atoi(params.Get("keepalive")); err == nil && keepalive > 0 {
			peer["persistent_keepalive_interval"] = keepalive
		}
		// WARP and its relatives prepend three reserved bytes to every packet.
		if reserved := parseReserved(params.Get("reserved")); reserved != nil {
			peer["reserved"] = reserved
		}

		outbound["type"] = "wireguard"
		outbound["address"] = addresses
		outbound["private_key"] = u.User.Username()
		outbound["peers"] = []interface{}{peer}
		if mtu, err := strconv.Atoi(params.Get("mtu")); err == nil && mtu > 0 {
			outbound["mtu"] = mtu
		}
		return outbound, nil

	case "http":
		// Plain HTTP CONNECT proxy. ProtocolOf only reaches this case for links
		// shaped like a proxy (explicit port, no path) — see isHTTPProxyLink.
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 80
		}
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		if u.User != nil {
			if username := u.User.Username(); username != "" {
				outbound["username"] = username
			}
			if password, ok := u.User.Password(); ok && password != "" {
				outbound["password"] = password
			}
		}

		// An HTTP proxy reached over TLS (Clash calls it "https"). The scheme
		// cannot carry that, so it travels as a query parameter.
		params := u.Query()
		if tlsParam := params.Get("tls"); tlsParam == "1" || tlsParam == "true" {
			tlsMap := make(map[string]interface{})
			tlsMap["enabled"] = true
			sni := params.Get("sni")
			if sni == "" {
				sni = u.Hostname()
			}
			tlsMap["server_name"] = sni
			if insecure := params.Get("insecure"); insecure == "1" || insecure == "true" {
				tlsMap["insecure"] = true
			}
			outbound["tls"] = tlsMap
		}
		return outbound, nil
	}

	return nil, fmt.Errorf("unsupported proxy protocol: %s", protocol)
}

// GenerateConfig generates a complete sing-box configuration map.
//
// The caller's outbound map is never modified: domain_resolver injection and
// server IP resolution happen on a shallow copy, because the same map is
// reused elsewhere (PingServer parses the same link).
//
// Type assertions on outbound fields use the two-value form throughout and
// report a descriptive error rather than panicking: the map is built from a
// user-supplied link and its shape cannot be assumed.
//
// clashSecret is injected into the Clash API config so only NeoBox itself can
// communicate with the local API endpoint on port 9097. Pass an empty string to
// disable authentication (not recommended in production).
func GenerateConfig(outbound map[string]interface{}, settings Settings, useSystemProxy bool, cacheDBPath string, clashSecret string) (map[string]interface{}, error) {
	tunMode := settings.TunMode

	// Validate the required fields before doing any work.
	outboundTag, ok := outbound["tag"].(string)
	if !ok || outboundTag == "" {
		return nil, fmt.Errorf("outbound map is missing required string field 'tag'")
	}

	// Work on a shallow copy so the caller's map is left untouched.
	workOutbound := make(map[string]interface{}, len(outbound))
	for k, v := range outbound {
		workOutbound[k] = v
	}

	// 1. DNS Section (Nuclear Strategy: No local DNS, only IP-based DoH for remote, local for direct)
	dnsServers := []map[string]interface{}{
		{
			"type":   "https",
			"tag":    "dns-remote",
			"server": "1.1.1.1",
			"path":   "/dns-query",
			"detour": outboundTag,
			"tls": map[string]interface{}{
				"enabled":     true,
				"server_name": "cloudflare-dns.com",
			},
		},
		{
			"type": "local",
			"tag":  "dns-direct",
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
	//
	// A WireGuard endpoint keeps its server address inside the first peer
	// instead of a top-level field, and that peer is a nested map the shallow
	// copy above still shares with the caller — so it is copied before being
	// rewritten, or resolving would reach back into the caller's outbound.
	serverDomain, _ := workOutbound["server"].(string)
	workOutbound["domain_resolver"] = "dns-direct"
	wgPeer := copyFirstWireGuardPeer(workOutbound)
	if wgPeer != nil {
		serverDomain, _ = wgPeer["address"].(string)
	}
	if net.ParseIP(serverDomain) == nil {
		resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer resolveCancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, serverDomain)
		if err == nil {
			for _, addr := range addrs {
				if addr.IP.To4() != nil {
					if wgPeer != nil {
						wgPeer["address"] = addr.IP.String()
					} else {
						workOutbound["server"] = addr.IP.String()
					}
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
		// Turn away NAT-PMP. In TUN mode the tunnel becomes the default gateway,
		// so anything on the machine looking for a port-mapping gateway aims its
		// requests at the tunnel's own address — and nothing here can answer,
		// because sing-box is not a NAT-PMP gateway.
		//
		// Unanswered, those requests repeat, and each one arrives from a fresh
		// source port: a packet capture measured 5374 distinct source ports
		// across 5452 packets in eight seconds. Every one of them is a new
		// connection to sing-box — rule evaluation, process lookup, a socket, a
		// NAT entry — which is why 680 tiny packets a second cost 1.4 CPU cores
		// while carrying no traffic at all.
		//
		// Rejecting with an ICMP error rather than dropping is deliberate: a
		// silent drop looks like packet loss and invites more retries, while the
		// error tells the sender there is no gateway here and lets it stop.
		//
		// Sits after the DNS hijack because the tunnel's address doubles as the
		// DNS server, and DNS must be intercepted before anything is refused.
		{"network": "udp", "port": []int{5351}, "action": "reject", "method": "default"},
	}

	// Block IPv6 to prevent leaks.
	// Default: true (protected). Only skip if user explicitly set ipv6Leak=false.
	if boolDefault(settings.Ipv6LeakProtection, true) {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_version": 6, "action": "reject",
		})
	}

	// Exclude proxy server IP or domain from proxy tunnel
	serverIPStr, _ := workOutbound["server"].(string)
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

	// Custom user-defined routing rules (take priority over geoip/geosite)
	for _, rule := range settings.CustomRules {
		if rule.Value == "" || rule.Type == "" || rule.Action == "" {
			continue
		}
		var outbound string
		switch rule.Action {
		case "direct":
			outbound = "direct"
		case "block":
			outbound = "block"
		case "proxy":
			outbound = outboundTag
		default:
			continue
		}
		ruleEntry := map[string]interface{}{
			"action":   "route",
			"outbound": outbound,
		}
		switch rule.Type {
		case "domain":
			ruleEntry["domain"] = []string{rule.Value}
		case "domain_suffix":
			ruleEntry["domain_suffix"] = []string{rule.Value}
		case "domain_keyword":
			ruleEntry["domain_keyword"] = []string{rule.Value}
		case "ip_cidr":
			ruleEntry["ip_cidr"] = []string{rule.Value}
		default:
			continue
		}
		routeRules = append(routeRules, ruleEntry)
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
				"outbound":     outboundTag,
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
			"outbound": outboundTag,
		})
	}

	// 4. Final Config Structure
	// FIX: Use ip_cidr when serverIPStr is an IP address, domain otherwise.
	// Placing an IP address in the "domain" field is logically incorrect for sing-box.
	firstDNSRule := map[string]interface{}{
		"domain_suffix": []string{".local", ".localhost"},
		"domain":        []string{"localhost", "wails.localhost"},
		"server":        "dns-direct",
	}
	if net.ParseIP(serverIPStr) != nil {
		// Server was resolved to an IP — exclude it via ip_cidr, not domain.
		firstDNSRule["ip_cidr"] = []string{serverIPStr + "/32"}
	} else if serverIPStr != "" {
		// Server is still a domain name — add it to the domain list.
		domains := firstDNSRule["domain"].([]string)
		firstDNSRule["domain"] = append(domains, serverIPStr)
	}
	dnsRules := []map[string]interface{}{firstDNSRule}
	if settings.FakeDns && tunMode {
		dnsRules = append(dnsRules, map[string]interface{}{
			"query_type": []string{"A", "AAAA"},
			"action":     "route",
			"server":     "dns-fake",
		})
	} else if boolDefault(settings.Ipv6LeakProtection, true) {
		// When IPv6 leak protection is on and fakeip is not used, reject AAAA queries
		// via dns-remote to prevent out-of-tunnel IPv6 DNS leaks.
		dnsRules = append(dnsRules, map[string]interface{}{
			"query_type": []string{"AAAA"},
			"action":     "route",
			"server":     "dns-remote",
		})
	}

	// "warn" keeps the Logs tab to things that actually need attention. "info"
	// adds a line per connection open and close, which the user opts into.
	logLevel := "warn"
	if settings.VerboseLogging {
		logLevel = "info"
	}

	config := map[string]interface{}{
		"log": map[string]interface{}{
			"level":     logLevel,
			"timestamp": true,
			// Send the core's own log stream to the null device. With "output"
			// unset sing-box writes every line to os.Stderr, and InitCrashLog
			// has replaced stderr with crash.log so that a panic in a core
			// goroutine leaves a trace on disk. The two combined turned a file
			// meant for crash traces into a transcript of every connection --
			// it reached 728 MB in a single session.
			//
			// Nothing is lost: the UI receives every line through the platform
			// log writer (see service.logStreamer), which is also what "Save
			// logs" writes out. Pointing at a real file instead would just move
			// the unbounded growth somewhere else.
			"output": os.DevNull,
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
				"listen":           ProxyListenHost,
				"listen_port":      ProxyListenPort,
				"set_system_proxy": useSystemProxy,
			},
		},
		"outbounds": []interface{}{
			workOutbound,
			map[string]interface{}{"type": "direct", "tag": "direct"},
			map[string]interface{}{"type": "block", "tag": "block"},
		},
		"route": map[string]interface{}{
			"rules":                 routeRules,
			"auto_detect_interface": true,
			// When DNS leak protection is on (default), resolve all domains via the VPN tunnel.
			// When the user explicitly disables it, fall back to local system resolver.
			"default_domain_resolver": func() string {
				if boolDefault(settings.DnsLeakProtection, true) {
					return "dns-remote"
				}
				return "dns-direct"
			}(),
			"final": outboundTag,
		},
		"experimental": map[string]interface{}{
			"cache_file": map[string]interface{}{
				"enabled":      true,
				"path":         cacheDBPath,
				"store_fakeip": true,
			},
			"clash_api": map[string]interface{}{
				"external_controller": ClashAPIAddr,
				// Security: always require a per-session secret so other processes on
				// the machine cannot control the VPN core via the local Clash API.
				"secret": clashSecret,
			},
		},
	}

	// WireGuard is not an outbound. sing-box 1.11 moved it into its own
	// "endpoints" section, and a "wireguard" outbound is now a stub that refuses
	// to start. Nothing else about the config changes: route.final still names
	// the tag, and the remote DNS server still detours through it.
	if outboundType, _ := workOutbound["type"].(string); outboundType == "wireguard" {
		config["endpoints"] = []interface{}{workOutbound}
		config["outbounds"] = []interface{}{
			map[string]interface{}{"type": "direct", "tag": "direct"},
			map[string]interface{}{"type": "block", "tag": "block"},
		}
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
			"auto_route":     true,
			"strict_route":   true,
			// "mixed" runs TCP through the system stack and keeps gVisor for
			// UDP. It is what sing-tun itself picks when no stack is named and
			// gVisor is compiled in (see sing-tun/stack.go), so it is the best
			// travelled path, and it takes TCP -- the bulk of the traffic -- out
			// of the userspace network stack entirely.
			//
			// Note the side effect: the system stack forwards over real sockets
			// and so adds a Windows Firewall rule allowing inbound TCP for this
			// executable (sing-tun/stack_system.go calls fixWindowsFirewall).
			// The system stack also needs one address beyond the first in each
			// prefix for its NAT; the /30 and /126 above provide it.
			"stack": "mixed",
			// sing-box's own default. The previous 1280 -- the IPv6 minimum, a
			// value that is never wrong and rarely right -- meant roughly seven
			// times as many packets for the same bytes, and every one of them
			// paid a full stack traversal.
			"mtu": 9000,
		})
		config["inbounds"] = inboundsSection
	}

	return config, nil
}

// errSubBlocked marks a response that arrived intact but carried no
// subscription: an HTML page or an empty body. The failure is content-level
// rather than transport-level, which makes it the only one worth retrying with
// a different User-Agent.
var errSubBlocked = errors.New("returned HTML or empty data")

// fetchSubscriptionBody performs a single GET and returns the body only when it
// looks like subscription data.
func fetchSubscriptionBody(client *http.Client, subURL, userAgent string) ([]byte, error) {
	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("response read error: %w", err)
	}

	trimmed := strings.ToLower(strings.TrimSpace(string(body)))
	if trimmed == "" || strings.Contains(trimmed, "<html") || strings.Contains(trimmed, "<script") {
		return nil, errSubBlocked
	}
	return body, nil
}

// FetchSubscription loads subscription contents (both JSON-based xray-ext and standard lists).
//
// Security: HTTP (non-TLS) subscription URLs are rejected to prevent MITM attacks
// where an attacker on the same network could substitute malicious proxy servers.
// Use HTTPS URLs for all subscriptions.
//
// URLs longer than maxLinkLength are rejected before the request is made.
func FetchSubscription(subURL string) ([]string, error) {
	trimmedURL := strings.TrimSpace(subURL)

	if len(trimmedURL) > maxLinkLength {
		return nil, errors.New(i18n.T(i18n.ErrSubURLTooLong, maxLinkLength))
	}

	trimmedURL = strings.ReplaceAll(trimmedURL, " ", "%20")
	trimmedURL = strings.ReplaceAll(trimmedURL, "\t", "%09")
	lowerURL := strings.ToLower(trimmedURL)

	// Security: block plain HTTP subscription URLs — only HTTPS is allowed.
	// Direct proxy links (vless://, vmess://, etc.) bypass this check.
	if strings.HasPrefix(lowerURL, "http://") {
		return nil, errors.New(i18n.T(i18n.ErrSubURLNotSecure))
	}
	if IsProxyLink(trimmedURL) {
		return []string{trimmedURL}, nil
	}

	// Transports, in the order they are tried: the local proxy first (highly
	// likely to succeed while the VPN is connected), then a direct request.
	type transport struct {
		name   string
		client *http.Client
	}
	var transports []transport
	if proxyURL, err := url.Parse("http://" + ProxyListenAddr); err == nil {
		transports = append(transports, transport{"proxy", &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}})
	}
	transports = append(transports, transport{"direct", &http.Client{
		Timeout: 15 * time.Second,
	}})

	// Each transport asks with a browser User-Agent first. Only when the host
	// answers with an HTML page or nothing at all — the signature of a panel
	// that sniffs the User-Agent — is the request repeated announcing a proxy
	// client, which is the one thing a different UA can fix.
	userAgents := []string{getRandomUserAgent(), subscriptionClientUA}

	var bodyBytes []byte
	var fetchErrs []string
	anyBlocked := false

fetch:
	for _, t := range transports {
		for _, ua := range userAgents {
			body, err := fetchSubscriptionBody(t.client, trimmedURL, ua)
			if err == nil {
				bodyBytes = body
				break fetch
			}
			fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", t.name, err))
			if !errors.Is(err, errSubBlocked) {
				break // the transport itself failed; another UA will not help
			}
			anyBlocked = true
		}
	}

	if bodyBytes == nil {
		// The two failures need different advice, and one page beats any number
		// of transport errors: a host that answers with a page is reachable and
		// the subscription itself is the problem, while an unreachable host says
		// nothing about the subscription at all.
		if anyBlocked {
			return nil, errors.New(i18n.T(i18n.ErrSubBlockedByHost))
		}
		return nil, errors.New(i18n.T(i18n.ErrSubUnreachable, strings.Join(fetchErrs, "; ")))
	}
	rawData := strings.TrimSpace(string(bodyBytes))

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
							case string:
								portVal, _ = strconv.Atoi(p)
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
							case string:
								portVal, _ = strconv.Atoi(p)
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

	// sing-box's own format: either a whole config object with an "outbounds"
	// list, or a bare array of outbounds that the xray-ext branch above did not
	// recognise as its own.
	if strings.HasPrefix(rawData, "{") || strings.HasPrefix(rawData, "[") {
		if links := parseSingboxOutbounds(bodyBytes); len(links) > 0 {
			return links, nil
		}
	}

	// Clash YAML, as served by most panels and sub-converters.
	if looksLikeClashYAML(rawData) {
		if links := parseClashYAML(bodyBytes); len(links) > 0 {
			return links, nil
		}
	}

	// Otherwise parse as standard newline separated plain text list (sometimes base64 encoded)
	// First, check if the entire payload is a single-line or multi-line base64 block
	cleanRawData := strings.ReplaceAll(rawData, "\r", "")
	cleanRawData = strings.ReplaceAll(cleanRawData, "\n", "")
	cleanRawData = strings.ReplaceAll(cleanRawData, " ", "")
	cleanRawData = strings.ReplaceAll(cleanRawData, "\t", "")

	if !strings.Contains(cleanRawData, "://") && cleanRawData != "" {
		if decodedBytes, err := decodeBase64Any(cleanRawData); err == nil {
			rawData = string(decodedBytes)
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
		if IsProxyLink(trimmed) {
			parsedLinks = append(parsedLinks, trimmed)
			continue
		}

		// Otherwise, try to decode this individual line from base64 (MIME/line-by-line format)
		if decodedBytes, err := decodeBase64Any(trimmed); err == nil {
			decodedStr := strings.TrimSpace(string(decodedBytes))
			if strings.Contains(decodedStr, "://") {
				parsedLinks = append(parsedLinks, decodedStr)
			}
		}
	}

	if len(parsedLinks) == 0 {
		// The download worked, so the payload is in a shape nothing here
		// recognises — or the panel handed back an empty list.
		return nil, errors.New(i18n.T(i18n.ErrSubNoNodes))
	}
	return parsedLinks, nil
}
