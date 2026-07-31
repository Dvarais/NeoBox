package core

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Subscription formats that are not a list of share links: sing-box JSON
// configs and Clash YAML. Both are converted into the links NeoBox stores, so
// everything downstream — ParseProxyLink, the tray, the server list — keeps
// seeing nothing but links, and no format knowledge leaks out of this file.

// node is the protocol-agnostic middle ground between a subscription entry and
// a share link. Both converters fill it in and node.link() emits a link that
// ParseProxyLink can read back, which keeps the field mapping (Clash's
// "servername", sing-box's "server_name", the link's "sni") in one place per
// format instead of one place per protocol per format.
type node struct {
	protocol string // canonical name, as returned by ProtocolOf
	name     string
	server   string
	port     int

	uuid     string // vless, vmess, tuic
	password string // trojan, ss, tuic, hysteria*, anytls, socks, http
	username string // socks, http
	method   string // ss cipher
	flow     string // vless

	tls         bool
	reality     bool
	sni         string
	fingerprint string
	alpn        []string
	insecure    bool
	publicKey   string
	shortID     string

	network     string // tcp, ws, grpc, httpupgrade, http
	path        string
	host        string
	serviceName string

	plugin     string // ss, SIP003
	pluginOpts string

	privateKey    string // wireguard
	peerPublicKey string // the peer's key, not reality's publicKey above
	preSharedKey  string
	addresses     []string
	reserved      []int
	mtu           int
	keepalive     int

	obfs         string // hysteria2
	obfsPassword string
	congestion   string // tuic
	up, down     string // hysteria v1
}

// sip003Plugins are the only SIP003 plugins the bundled sing-box registers, and
// the aliases each is known by. A node using anything else — shadow-tls,
// restls, gost-plugin — is dropped rather than imported, since the core would
// refuse it at connect time.
var sip003Plugins = map[string]string{
	"obfs-local":               "obfs-local",
	"simple-obfs":              "obfs-local",
	"obfs":                     "obfs-local",
	"v2ray-plugin":             "v2ray-plugin",
	"v2ray":                    "v2ray-plugin",
	"shadowsocks-v2ray-plugin": "v2ray-plugin",
}

// parsePluginParam splits a link's "plugin" parameter into the plugin name and
// the options string sing-box expects, canonicalising the name. An unsupported
// or empty plugin yields an empty name.
func parsePluginParam(param string) (name, opts string) {
	if param == "" {
		return "", ""
	}
	name, opts, _ = strings.Cut(param, ";")
	canonical, supported := sip003Plugins[strings.ToLower(strings.TrimSpace(name))]
	if !supported {
		return "", ""
	}
	return canonical, opts
}

// escapePluginValue escapes the three characters that separate one plugin
// option from the next, so a value containing them survives the round trip
// through sing-box's SIP003 argument parser.
func escapePluginValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `=`, `\=`)
	return replacer.Replace(value)
}

// linkSchemes maps a canonical protocol onto the scheme its share link uses.
// Only socks differs: sing-box calls the outbound "socks" while links are
// written "socks5://".
var linkSchemes = map[string]string{
	"vless":     "vless",
	"vmess":     "vmess",
	"ss":        "ss",
	"trojan":    "trojan",
	"tuic":      "tuic",
	"hysteria":  "hysteria",
	"hysteria2": "hysteria2",
	"anytls":    "anytls",
	"socks":     "socks5",
	"http":      "http",
	"wireguard": "wireguard",
}

// link renders the node as a share link, or "" when the node is too incomplete
// to connect to.
func (n node) link() string {
	scheme, known := linkSchemes[n.protocol]
	if !known || n.server == "" || n.port <= 0 {
		return ""
	}
	if n.protocol == "vmess" {
		return n.vmessLink()
	}

	params := url.Values{}
	var user *url.Userinfo

	switch n.protocol {
	case "vless":
		if n.uuid == "" {
			return ""
		}
		user = url.User(n.uuid)
		if n.flow != "" {
			params.Set("flow", n.flow)
		}
	case "trojan", "hysteria2", "anytls":
		user = url.User(n.password)
	case "ss":
		if n.method == "" {
			return ""
		}
		user = url.UserPassword(n.method, n.password)
		if n.plugin != "" {
			// SIP002 carries the plugin and its options in a single parameter.
			plugin := n.plugin
			if n.pluginOpts != "" {
				plugin += ";" + n.pluginOpts
			}
			params.Set("plugin", plugin)
		}
	case "tuic":
		user = url.UserPassword(n.uuid, n.password)
	case "socks", "http":
		if n.username != "" || n.password != "" {
			user = url.UserPassword(n.username, n.password)
		}
	case "hysteria":
		if n.password != "" {
			params.Set("auth", n.password)
		}
	case "wireguard":
		// Without the interface address and the peer's key there is no tunnel
		// to build, so such an entry is dropped rather than stored broken.
		if n.privateKey == "" || n.peerPublicKey == "" || len(n.addresses) == 0 {
			return ""
		}
		user = url.User(n.privateKey)
		params.Set("publickey", n.peerPublicKey)
		params.Set("address", strings.Join(n.addresses, ","))
		if n.preSharedKey != "" {
			params.Set("presharedkey", n.preSharedKey)
		}
		if n.mtu > 0 {
			params.Set("mtu", strconv.Itoa(n.mtu))
		}
		if n.keepalive > 0 {
			params.Set("keepalive", strconv.Itoa(n.keepalive))
		}
		if len(n.reserved) == 3 {
			reserved := make([]string, 0, 3)
			for _, b := range n.reserved {
				reserved = append(reserved, strconv.Itoa(b))
			}
			params.Set("reserved", strings.Join(reserved, ","))
		}
	}

	switch n.protocol {
	case "vless", "trojan":
		network := n.network
		if network == "" {
			network = "tcp"
		}
		params.Set("type", network)
		switch network {
		case "ws", "httpupgrade":
			if n.path != "" {
				params.Set("path", n.path)
			}
			if n.host != "" {
				params.Set("host", n.host)
			}
		case "grpc":
			if n.serviceName != "" {
				params.Set("serviceName", n.serviceName)
			}
		case "http", "h2":
			if n.path != "" {
				params.Set("path", n.path)
			}
			if n.host != "" {
				params.Set("host", n.host)
			}
		}

		switch {
		case n.reality:
			params.Set("security", "reality")
			params.Set("pbk", n.publicKey)
			if n.shortID != "" {
				params.Set("sid", n.shortID)
			}
		case n.tls:
			params.Set("security", "tls")
		default:
			params.Set("security", "none")
		}
		if n.fingerprint != "" {
			params.Set("fp", n.fingerprint)
		}

	case "hysteria2":
		if n.obfs != "" {
			params.Set("obfs", n.obfs)
			if n.obfsPassword != "" {
				params.Set("obfs-password", n.obfsPassword)
			}
		}

	case "hysteria":
		if n.up != "" {
			params.Set("up", n.up)
		}
		if n.down != "" {
			params.Set("down", n.down)
		}

	case "tuic":
		if n.congestion != "" {
			params.Set("congestion_control", n.congestion)
		}

	case "http":
		// The scheme cannot say whether the proxy speaks TLS, so it travels as
		// a parameter — see the "http" case in ParseProxyLink.
		if n.tls {
			params.Set("tls", "1")
		}
	}

	// TLS details every TLS-bearing protocol shares. They are harmless on the
	// ones that ignore them.
	if n.sni != "" {
		params.Set("sni", n.sni)
	}
	if len(n.alpn) > 0 {
		params.Set("alpn", strings.Join(n.alpn, ","))
	}
	if n.insecure {
		params.Set("insecure", "1")
	}

	u := url.URL{
		Scheme: scheme,
		User:   user,
		Host:   net.JoinHostPort(strings.Trim(n.server, "[]"), strconv.Itoa(n.port)),
	}
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	link := u.String()
	if n.name != "" {
		// QueryEscape rather than the URL type's own fragment encoding, because
		// ParseProxyLink reads the name back with url.QueryUnescape.
		link += "#" + url.QueryEscape(n.name)
	}
	return link
}

// vmessLink renders the base64-of-JSON form VMess links use.
func (n node) vmessLink() string {
	if n.uuid == "" {
		return ""
	}
	network := n.network
	if network == "" {
		network = "tcp"
	}
	data := map[string]interface{}{
		"v":    "2",
		"ps":   n.name,
		"add":  n.server,
		"port": strconv.Itoa(n.port),
		"id":   n.uuid,
		"aid":  "0",
		"scy":  "auto",
		"net":  network,
		"type": "none",
	}
	if n.tls {
		data["tls"] = "tls"
		if n.sni != "" {
			data["sni"] = n.sni
		}
		if n.fingerprint != "" {
			data["fp"] = n.fingerprint
		}
	}
	switch network {
	case "ws", "httpupgrade", "http", "h2":
		if n.path != "" {
			data["path"] = n.path
		}
		if n.host != "" {
			data["host"] = n.host
		}
	case "grpc":
		if n.serviceName != "" {
			data["path"] = n.serviceName
		}
	}

	encoded, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	// URL-safe and unpadded: a "/" from standard base64 would end the authority
	// and truncate the payload. ParseProxyLink accepts either alphabet.
	return "vmess://" + base64.RawURLEncoding.EncodeToString(encoded)
}

// ─── sing-box JSON ──────────────────────────────────────────────────────────

// parseSingboxOutbounds converts the proxy outbounds of a sing-box config into
// share links. It accepts both a whole config object ({"outbounds": [...]}) and
// a bare array of outbounds. Non-proxy outbounds — selector, urltest, direct,
// block, dns — are skipped.
func parseSingboxOutbounds(payload []byte) []string {
	var outbounds []map[string]interface{}

	var config map[string]interface{}
	if err := json.Unmarshal(payload, &config); err == nil {
		// "endpoints" as well as "outbounds": since sing-box 1.11 WireGuard
		// lives there, and a config that carries one would otherwise import as
		// an empty subscription.
		for _, section := range []string{"outbounds", "endpoints"} {
			raw, _ := config[section].([]interface{})
			for _, item := range raw {
				if ob, ok := item.(map[string]interface{}); ok {
					outbounds = append(outbounds, ob)
				}
			}
		}
	} else if err := json.Unmarshal(payload, &outbounds); err != nil {
		return nil
	}

	var links []string
	for _, ob := range outbounds {
		if link := singboxOutboundToLink(ob); link != "" {
			links = append(links, link)
		}
	}
	return links
}

// singboxOutboundToLink converts one sing-box outbound into a share link.
func singboxOutboundToLink(ob map[string]interface{}) string {
	n := node{
		protocol: fieldString(ob, "type"),
		name:     fieldString(ob, "tag"),
		server:   fieldString(ob, "server"),
		port:     fieldInt(ob, "server_port"),
		uuid:     fieldString(ob, "uuid"),
		password: fieldString(ob, "password"),
		username: fieldString(ob, "username"),
		method:   fieldString(ob, "method"),
		flow:     fieldString(ob, "flow"),
	}
	if n.protocol == "shadowsocks" {
		n.protocol = "ss"
		if plugin, supported := sip003Plugins[fieldString(ob, "plugin")]; supported {
			n.plugin = plugin
			n.pluginOpts = fieldString(ob, "plugin_opts")
		} else if fieldString(ob, "plugin") != "" {
			return "" // a plugin the core cannot load makes the node unusable
		}
	}
	if n.protocol == "hysteria2" {
		// Hysteria2 obfuscation is a nested object rather than a flat field.
		if obfs := fieldMap(ob, "obfs"); obfs != nil {
			n.obfs = fieldString(obfs, "type")
			n.obfsPassword = fieldString(obfs, "password")
		}
	}
	if n.protocol == "hysteria" {
		n.password = fieldString(ob, "auth_str")
		n.up = fieldString(ob, "up")
		n.down = fieldString(ob, "down")
	}
	n.congestion = fieldString(ob, "congestion_control")

	if n.protocol == "wireguard" {
		n.privateKey = fieldString(ob, "private_key")
		n.mtu = fieldInt(ob, "mtu")
		n.addresses = fieldStrings(ob, "address")
		// The server lives in the first peer, not in a top-level field.
		if peers, _ := ob["peers"].([]interface{}); len(peers) > 0 {
			if peer, ok := peers[0].(map[string]interface{}); ok {
				n.server = fieldString(peer, "address")
				n.port = fieldInt(peer, "port")
				n.peerPublicKey = fieldString(peer, "public_key")
				n.preSharedKey = fieldString(peer, "pre_shared_key")
				n.keepalive = fieldInt(peer, "persistent_keepalive_interval")
				n.reserved = clashReserved(peer["reserved"])
			}
		}
	}

	if tls := fieldMap(ob, "tls"); tls != nil && fieldBool(tls, "enabled") {
		n.tls = true
		n.sni = fieldString(tls, "server_name")
		n.insecure = fieldBool(tls, "insecure")
		n.alpn = fieldStrings(tls, "alpn")
		if utls := fieldMap(tls, "utls"); utls != nil {
			n.fingerprint = fieldString(utls, "fingerprint")
		}
		if reality := fieldMap(tls, "reality"); reality != nil && fieldBool(reality, "enabled") {
			n.reality = true
			n.publicKey = fieldString(reality, "public_key")
			n.shortID = fieldString(reality, "short_id")
		}
	}

	if transport := fieldMap(ob, "transport"); transport != nil {
		n.network = fieldString(transport, "type")
		n.path = fieldString(transport, "path")
		n.serviceName = fieldString(transport, "service_name")
		n.host = fieldString(transport, "host")
		if n.host == "" {
			if headers := fieldMap(transport, "headers"); headers != nil {
				n.host = fieldString(headers, "Host")
			}
		}
	}

	return n.link()
}

// ─── Clash YAML ─────────────────────────────────────────────────────────────

// clashProxyTypes maps a Clash proxy type onto NeoBox's canonical protocol.
// Types absent here (ssr, snell, wireguard, ssh, …) cannot be run by the
// bundled sing-box and are skipped rather than imported as dead entries.
var clashProxyTypes = map[string]string{
	"ss":        "ss",
	"vmess":     "vmess",
	"vless":     "vless",
	"trojan":    "trojan",
	"tuic":      "tuic",
	"hysteria":  "hysteria",
	"hysteria2": "hysteria2",
	"hy2":       "hysteria2",
	"anytls":    "anytls",
	"socks5":    "socks",
	"socks":     "socks",
	"http":      "http",
	"https":     "http",
	"wireguard": "wireguard",
}

// looksLikeClashYAML reports whether a payload is a Clash configuration. The
// "proxies:" key at the start of a line is what every Clash subscription has
// and what no base64 blob or link list can contain.
func looksLikeClashYAML(rawData string) bool {
	return strings.HasPrefix(rawData, "proxies:") || strings.Contains(rawData, "\nproxies:")
}

// parseClashYAML converts the proxies of a Clash configuration into links.
func parseClashYAML(payload []byte) []string {
	var config struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(payload, &config); err != nil {
		return nil
	}

	var links []string
	for _, proxy := range config.Proxies {
		if link := clashProxyToLink(proxy); link != "" {
			links = append(links, link)
		}
	}
	return links
}

// clashProxyToLink converts one entry of a Clash "proxies:" list into a link.
func clashProxyToLink(proxy map[string]interface{}) string {
	protocol, supported := clashProxyTypes[strings.ToLower(fieldString(proxy, "type"))]
	if !supported {
		return ""
	}

	n := node{
		protocol: protocol,
		name:     fieldString(proxy, "name"),
		server:   fieldString(proxy, "server"),
		port:     fieldInt(proxy, "port"),
		uuid:     fieldString(proxy, "uuid"),
		password: fieldString(proxy, "password"),
		username: fieldString(proxy, "username"),
		method:   fieldString(proxy, "cipher"),
		flow:     fieldString(proxy, "flow"),
		insecure: fieldBool(proxy, "skip-cert-verify"),
		alpn:     fieldStrings(proxy, "alpn"),
		network:  fieldString(proxy, "network"),
	}

	// Clash spells the SNI "servername" for VMess/VLESS and "sni" elsewhere.
	n.sni = fieldString(proxy, "servername")
	if n.sni == "" {
		n.sni = fieldString(proxy, "sni")
	}
	n.fingerprint = fieldString(proxy, "client-fingerprint")

	// TLS is implicit for the protocols that cannot run without it; for the
	// rest it is an explicit flag.
	switch protocol {
	case "trojan", "hysteria", "hysteria2", "tuic", "anytls":
		n.tls = true
	default:
		n.tls = fieldBool(proxy, "tls")
	}
	if strings.EqualFold(fieldString(proxy, "type"), "https") {
		n.tls = true
	}

	switch protocol {
	case "hysteria2":
		n.obfs = fieldString(proxy, "obfs")
		n.obfsPassword = fieldString(proxy, "obfs-password")
	case "hysteria":
		if n.password == "" {
			n.password = fieldString(proxy, "auth-str")
		}
		n.up = fieldString(proxy, "up")
		n.down = fieldString(proxy, "down")
	case "tuic":
		n.congestion = fieldString(proxy, "congestion-controller")
	case "wireguard":
		n.privateKey = fieldString(proxy, "private-key")
		n.peerPublicKey = fieldString(proxy, "public-key")
		n.preSharedKey = fieldString(proxy, "pre-shared-key")
		n.mtu = fieldInt(proxy, "mtu")
		n.keepalive = fieldInt(proxy, "persistent-keepalive")
		// Clash names the interface addresses "ip"/"ipv6" and writes them bare.
		for _, key := range []string{"ip", "ipv6"} {
			if address := fieldString(proxy, key); address != "" {
				n.addresses = append(n.addresses, withPrefixLength(address))
			}
		}
		n.reserved = clashReserved(proxy["reserved"])
	case "ss":
		if n.method == "" {
			n.method = fieldString(proxy, "method")
		}
		if rawPlugin := fieldString(proxy, "plugin"); rawPlugin != "" {
			plugin, supported := sip003Plugins[strings.ToLower(rawPlugin)]
			if !supported {
				return "" // shadow-tls and friends: the core cannot load them
			}
			n.plugin = plugin
			n.pluginOpts = clashPluginOpts(plugin, fieldMap(proxy, "plugin-opts"))
		}
	}

	if reality := fieldMap(proxy, "reality-opts"); reality != nil {
		n.reality = true
		n.tls = true
		n.publicKey = fieldString(reality, "public-key")
		n.shortID = fieldString(reality, "short-id")
	}

	switch n.network {
	case "ws":
		if ws := fieldMap(proxy, "ws-opts"); ws != nil {
			n.path = fieldString(ws, "path")
			if headers := fieldMap(ws, "headers"); headers != nil {
				n.host = fieldString(headers, "Host")
				if n.host == "" {
					n.host = fieldString(headers, "host")
				}
			}
		}
	case "grpc":
		if grpc := fieldMap(proxy, "grpc-opts"); grpc != nil {
			n.serviceName = fieldString(grpc, "grpc-service-name")
		}
	case "http", "h2":
		if h2 := fieldMap(proxy, "h2-opts"); h2 != nil {
			n.path = fieldString(h2, "path")
			if hosts := fieldStrings(h2, "host"); len(hosts) > 0 {
				n.host = hosts[0]
			}
		}
	}

	return n.link()
}

// clashReserved reads the WireGuard reserved bytes, which Clash writes either
// as a three-number list or as the base64 of the same bytes.
func clashReserved(value interface{}) []int {
	switch reserved := value.(type) {
	case []interface{}:
		if len(reserved) != 3 {
			return nil
		}
		out := make([]int, 0, 3)
		for _, item := range reserved {
			switch b := item.(type) {
			case int:
				out = append(out, b)
			case float64:
				out = append(out, int(b))
			default:
				return nil
			}
		}
		return out
	case string:
		return parseReserved(reserved)
	}
	return nil
}

// clashPluginOpts renders a Clash "plugin-opts:" block as the SIP003 options
// string sing-box parses. The two disagree on names — Clash calls the obfs mode
// "mode" where the plugin itself calls it "obfs" — so the mapping is explicit.
func clashPluginOpts(plugin string, opts map[string]interface{}) string {
	if opts == nil {
		return ""
	}

	var parts []string
	add := func(key, value string) {
		if value != "" {
			parts = append(parts, key+"="+escapePluginValue(value))
		}
	}

	switch plugin {
	case "obfs-local":
		add("obfs", fieldString(opts, "mode"))
		add("obfs-host", fieldString(opts, "host"))
	case "v2ray-plugin":
		add("mode", fieldString(opts, "mode"))
		add("host", fieldString(opts, "host"))
		add("path", fieldString(opts, "path"))
		if fieldBool(opts, "tls") {
			// A bare flag, not a key=value pair: the plugin only checks whether
			// "tls" is present.
			parts = append(parts, "tls")
		}
		if fieldBool(opts, "mux") {
			add("mux", "8")
		}
	}
	return strings.Join(parts, ";")
}

// ─── field readers ──────────────────────────────────────────────────────────
//
// Both formats arrive as map[string]interface{} with values whose Go type
// depends on the decoder — YAML yields int and bool, JSON yields float64 and
// string — so every read goes through these instead of a bare type assertion.

func fieldString(m map[string]interface{}, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func fieldInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func fieldBool(m map[string]interface{}, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	case int:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

func fieldMap(m map[string]interface{}, key string) map[string]interface{} {
	if sub, ok := m[key].(map[string]interface{}); ok {
		return sub
	}
	return nil
}

func fieldStrings(m map[string]interface{}, key string) []string {
	switch v := m[key].(type) {
	case []interface{}:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		return strings.Split(v, ",")
	}
	return nil
}
