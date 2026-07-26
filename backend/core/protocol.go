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
}

// ProtocolOf returns the canonical protocol of a proxy link, or "" when the link
// does not use a scheme NeoBox supports. Matching is case-insensitive.
func ProtocolOf(link string) string {
	s := strings.TrimSpace(link)
	i := strings.Index(s, "://")
	if i <= 0 {
		return ""
	}
	return proxySchemes[strings.ToLower(s[:i])]
}

// IsProxyLink reports whether link uses a proxy scheme NeoBox supports.
func IsProxyLink(link string) bool {
	return ProtocolOf(link) != ""
}
