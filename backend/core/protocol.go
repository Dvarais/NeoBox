package core

import "strings"

// maxLinkLength caps a proxy link or subscription URL. Anything longer is
// rejected before parsing, so a malformed or hostile entry cannot turn into
// unbounded work.
const maxLinkLength = 4096

// proxySchemes maps every URL scheme NeoBox accepts as a proxy link onto its
// canonical protocol name. Aliases collapse onto that canonical form, so callers
// never have to know that "hy2" and "hysteria2" are the same thing.
//
// This list used to be spelled out as a chain of strings.HasPrefix calls in four
// separate places — clipboard import, subscription fetching, per-line parsing
// and the tray menu — which drifted: the clipboard check was case-sensitive
// while the others lower-cased first, so a link pasted as "VLESS://…" was
// accepted by one path and silently dropped by another.
var proxySchemes = map[string]string{
	"vless":     "vless",
	"vmess":     "vmess",
	"ss":        "ss",
	"trojan":    "trojan",
	"tuic":      "tuic",
	"hysteria":  "hysteria",
	"hysteria2": "hysteria2",
	"hy2":       "hysteria2",
	"anytls":    "anytls",
	"socks":     "socks",
	"socks5":    "socks",
	"http":      "http",
	"wireguard": "wireguard",
	"wg":        "wireguard",
}

// ProtocolOf returns the canonical protocol of a proxy link, or "" when the link
// does not use a scheme NeoBox supports. Matching is case-insensitive.
func ProtocolOf(link string) string {
	s := strings.TrimSpace(link)
	i := strings.Index(s, "://")
	if i <= 0 {
		return ""
	}
	proto := proxySchemes[strings.ToLower(s[:i])]
	if proto == "http" && !isHTTPProxyLink(s[i+len("://"):]) {
		return ""
	}
	return proto
}

// isHTTPProxyLink reports whether the part of an http:// link after the scheme
// names an HTTP proxy rather than an ordinary web address.
//
// The scheme alone cannot tell the two apart — a subscription address and an
// HTTP proxy node are both spelled "http://" — so the shape decides: a proxy
// carries an explicit port and no path ("http://1.2.3.4:8080#Home"), while
// "http://example.com/sub" is a web address. Without this, every URL sitting in
// the clipboard would import as a server.
func isHTTPProxyLink(rest string) bool {
	// The #fragment is the node name and the ?query holds options; neither says
	// anything about the path.
	if i := strings.IndexAny(rest, "#?"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSuffix(rest, "/")
	if strings.Contains(rest, "/") {
		return false
	}
	hostPort := rest
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		hostPort = rest[at+1:] // drop user:password@
	}
	// An IPv6 literal is full of colons, so only the one after the closing
	// bracket can be the port separator.
	if b := strings.LastIndex(hostPort, "]"); b >= 0 {
		return strings.Contains(hostPort[b+1:], ":")
	}
	return strings.Contains(hostPort, ":")
}

// IsProxyLink reports whether link uses a proxy scheme NeoBox supports.
func IsProxyLink(link string) bool {
	return ProtocolOf(link) != ""
}

// udpOnlyProtocols carry every byte over UDP. Nothing listens on the matching
// TCP port, so dialling it measures the timeout rather than the server.
var udpOnlyProtocols = map[string]bool{
	"wireguard": true,
	"hysteria":  true,
	"hysteria2": true,
	"tuic":      true,
}

// IsUDPOnly reports whether a canonical protocol runs over UDP alone.
func IsUDPOnly(protocol string) bool {
	return udpOnlyProtocols[protocol]
}

// ServerEndpoint returns the host and port a parsed outbound connects to.
//
// Every protocol but one keeps them in "server" and "server_port". WireGuard is
// an endpoint rather than an outbound and holds its address in the first peer,
// which is why reading the top-level fields used to yield nothing for it.
func ServerEndpoint(outbound map[string]interface{}) (string, int) {
	if outboundType, _ := outbound["type"].(string); outboundType == "wireguard" {
		peers, _ := outbound["peers"].([]interface{})
		if len(peers) == 0 {
			return "", 0
		}
		peer, ok := peers[0].(map[string]interface{})
		if !ok {
			return "", 0
		}
		host, _ := peer["address"].(string)
		port, _ := peer["port"].(int)
		return host, port
	}

	host, _ := outbound["server"].(string)
	port, _ := outbound["server_port"].(int)
	return host, port
}
