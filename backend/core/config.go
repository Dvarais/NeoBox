package core

import (
	"NeoBox/backend/i18n"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	randv2 "math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	return realisticUserAgents[randv2.IntN(len(realisticUserAgents))]
}

// GetRandomUserAgent returns a random browser User-Agent string from the pool.
func GetRandomUserAgent() string {
	return getRandomUserAgent()
}

// dohProviders maps the resolver addresses the DNS dropdown offers to the
// DoH endpoint each one serves.
//
// The address is what settings.json stores — the dropdown's option values are
// bare IPs — and the hostname is what the TLS handshake needs. Keeping the IP
// as the address and carrying the hostname only as the SNI is the point of the
// whole arrangement: the resolver never has to be looked up, so there is no
// bootstrap query to leak before the tunnel's own DNS is up.
// Вторые адреса тех же резолверов и AdGuard добавлены потому, что их набирают
// по памяти не реже первых. Записаны только те соответствия, в которых нет
// сомнений: неверное имя здесь хуже его отсутствия — оно уедет в SNI, и
// рукопожатие развалится там, где без карты оно бы состоялось по IP-SAN.
//
// owner — владелец сети, от имени которой резолвер ходит к авторитетным
// серверам. Нужен проверке утечки DNS: она узнаёт, кто на самом деле обслужил
// запрос, и сверяет с тем, кого выбрал пользователь. Сверять по адресу нельзя —
// запросы принимаются на 1.1.1.1, а наружу провайдер ходит с совсем других
// адресов своей сети. Строка сравнивается без учёта регистра как подстрока
// имени организации из записи AS.
var dohProviders = map[string]struct{ hostname, path, owner string }{
	"1.1.1.1":         {"cloudflare-dns.com", "/dns-query", "cloudflare"},
	"1.0.0.1":         {"cloudflare-dns.com", "/dns-query", "cloudflare"},
	"8.8.8.8":         {"dns.google", "/dns-query", "google"},
	"8.8.4.4":         {"dns.google", "/dns-query", "google"},
	"9.9.9.9":         {"dns.quad9.net", "/dns-query", "quad9"},
	"149.112.112.112": {"dns.quad9.net", "/dns-query", "quad9"},
	"94.140.14.14":    {"dns.adguard-dns.com", "/dns-query", "adguard"},
}

// DNSResolverOwners отдаёт интерфейсу карту «адрес резолвера — владелец сети».
//
// Существует затем, чтобы проверка утечки не держала свою копию этого списка.
// Копия молча разошлась бы с оригиналом при первом же добавленном провайдере, и
// разошлась бы худшим образом: проверка объявила бы утечку на исправном
// туннеле, потому что владельца нового резолвера она бы просто не знала.
func DNSResolverOwners() map[string]string {
	owners := make(map[string]string, len(dohProviders))
	for address, provider := range dohProviders {
		owners[address] = provider.owner
	}
	return owners
}

// defaultDoHAddress is used when no DNS server has been chosen. It is the value
// the dropdown starts on, so an untouched install keeps exactly the behaviour it
// has always had.
const defaultDoHAddress = "1.1.1.1"

// remoteDNSServer builds the dns-remote server from the user's DNS setting.
//
// Until now this setting was written to settings.json by the UI and read by
// nobody: Settings.Dns was declared and never referenced anywhere in the
// backend, so the resolver was hardcoded to Cloudflare no matter what the user
// picked. Choosing Google, Quad9 or a DoH URL of their own changed the stored
// value, the dropdown, and nothing else.
//
// Two properties are preserved whatever the user asks for, because the rest of
// the DNS design rests on them:
//
//   - The server is always DoH. Plain DNS would be readable by anything on the
//     path, and config_generate_test asserts the type for that reason.
//   - It is always detoured through the proxy outbound, so queries leave inside
//     the tunnel rather than beside it.
//
// A value that cannot satisfy those is an error rather than a silent fallback.
// Silently ignoring the setting is the bug being fixed here; doing it again for
// a typo would leave the user in exactly the same place, watching a resolver
// they did not choose.
// isDoHHostname reports whether the value looks like a bare hostname that can
// be turned into a DoH URL.
//
// Голый IP сюда не попадает намеренно. Для DoH нужно имя, которым проверяется
// сертификат; у незнакомого адреса такого имени взять неоткуда, и подстановка
// самого адреса в SNI дала бы отказ рукопожатия вместо понятной ошибки.
// Известные провайдеры разобраны выше по карте dohProviders — там имя есть.
func isDoHHostname(value string) bool {
	if value == "" || strings.ContainsAny(value, " \t/\\?#@:[]") {
		return false
	}
	if !strings.Contains(value, ".") || strings.Contains(value, "..") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return false
	}
	return true
}

// ValidateDNSSetting проверяет значение поля «свой DNS» тем же кодом, что и
// генерация конфига, и возвращает пустую строку, если всё в порядке.
//
// Существует затем, чтобы интерфейс мог сказать о непригодном адресе сразу, а
// не через сборку конфига при нажатии «Подключиться». Проверка одна на оба
// пути: своя копия правил во фронтенде разошлась бы с этой при первой правке.
func ValidateDNSSetting(setting string) string {
	if _, err := remoteDNSServer(setting, "proxy"); err != nil {
		return err.Error()
	}
	return ""
}

func remoteDNSServer(setting, outboundTag string) (map[string]interface{}, error) {
	address := strings.TrimSpace(setting)
	if address == "" {
		address = defaultDoHAddress
	}

	server := map[string]interface{}{
		"type":   "https",
		"tag":    "dns-remote",
		"path":   "/dns-query",
		"detour": outboundTag,
	}

	// A known provider, named by the IP the dropdown stores.
	if provider, ok := dohProviders[address]; ok {
		server["server"] = address
		server["path"] = provider.path
		server["tls"] = map[string]interface{}{
			"enabled":     true,
			"server_name": provider.hostname,
		}
		return server, nil
	}

	// Голое имя хоста — самая естественная запись «своего DNS», и раньше она
	// отвергалась наравне с мусором: пользователь, написавший
	// dns.adguard-dns.com, получал совет «дайте один из 1.1.1.1, 8.8.8.8,
	// 9.9.9.9». Дописываем схему и путь по умолчанию сами — получается тот же
	// DoH, к которому всё и сводится.
	if !strings.Contains(address, "://") && isDoHHostname(address) {
		address = "https://" + address + "/dns-query"
	}

	// Голый IP, которого нет в карте провайдеров.
	//
	// Раньше он отвергался: имени для проверки сертификата взять неоткуда.
	// Но проверять можно и по IP-SAN — крупные резолверы такой SAN несут, —
	// поэтому адрес принимается и превращается в тот же DoH. SNI при этом не
	// отправляется вовсе, см. сборку блока tls ниже.
	//
	// Частные и петлевые адреса отсекаются здесь, а не молчаливым отказом
	// рукопожатия: резолвер в домашней сети через detour всё равно недостижим —
	// запрос уйдёт искать его в сети VPN-сервера, — и понятная причина лучше
	// невнятной ошибки TLS через минуту.
	if !strings.Contains(address, "://") {
		if addr, err := netip.ParseAddr(address); err == nil {
			if !isRoutableDestination(addr) {
				return nil, fmt.Errorf(
					"DNS server %q cannot be reached: it is a local or loopback address, and DNS "+
						"queries travel inside the tunnel, where that address belongs to the "+
						"server's network rather than yours", address)
			}
			if addr.Is6() {
				address = "https://[" + addr.String() + "]/dns-query"
			} else {
				address = "https://" + addr.String() + "/dns-query"
			}
		}
	}

	// Всё остальное обязано быть DoH-адресом. Требование не косметическое:
	// сервер уходит через detour в туннель, поэтому обычный DNS был бы виден
	// оператору сервера, а локальный резолвер просто не нашёлся бы — запрос
	// ушёл бы искать его в сети сервера, а не дома.
	if !strings.HasPrefix(address, "https://") {
		return nil, fmt.Errorf(
			"DNS server %q is not usable: give a hostname (dns.example.com), a DoH URL "+
				"(https://dns.example.com/dns-query), or one of %s. Plain DNS is not "+
				"accepted: it travels inside the tunnel, where the server operator would read it",
			address, strings.Join(sortedProviderAddresses(), ", "))
	}

	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("DNS server %q is not a valid URL", address)
	}

	host := parsed.Hostname()
	path := parsed.EscapedPath()
	if path == "" || path == "/" {
		path = "/dns-query"
	}
	server["server"] = host
	server["path"] = path
	if port := parsed.Port(); port != "" {
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return nil, fmt.Errorf("DNS server %q has an invalid port", address)
		}
		server["server_port"] = portNum
	}

	// SNI.
	//
	// Для имени это само имя. Для известного провайдера — имя из карты: его
	// несёт сертификат, и подстановка избавляет от заведомо неудачного
	// рукопожатия.
	//
	// Для незнакомого IP SNI не отправляется вовсе. Раньше туда уходил сам
	// адрес, и это было неверно: SNI по RFC 6066 — доменное имя, IP-литерал в
	// нём недопустим, сервер такой либо игнорирует, либо рвёт соединение. Без
	// SNI сертификат проверяется по IP-SAN; не несёт его сервер — рукопожатие
	// честно не состоится. Проверка при этом никуда не девается: отключать её
	// приложение не умеет и уметь не должно.
	tlsOpts := map[string]interface{}{"enabled": true}
	if provider, ok := dohProviders[host]; ok {
		tlsOpts["server_name"] = provider.hostname
	} else if net.ParseIP(host) == nil {
		tlsOpts["server_name"] = host
	}
	server["tls"] = tlsOpts

	// A hostname has to be resolved before it can be dialled, and the tunnel's
	// own resolver is the thing being defined here — so that first lookup goes
	// through the system resolver. It reveals the DoH provider's name to it,
	// once per session, and nothing else. Naming the resolver by IP avoids it
	// entirely, which is why the built-in choices do.
	if net.ParseIP(host) == nil {
		server["domain_resolver"] = "dns-direct"
	}
	return server, nil
}

// sortedProviderAddresses lists the built-in resolvers in a stable order, so
// the error message above does not shuffle between runs.
func sortedProviderAddresses() []string {
	addresses := make([]string, 0, len(dohProviders))
	for address := range dohProviders {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	return addresses
}

// resolveRemoteDNSHost заменяет имя DoH-резолвера его адресом.
//
// remoteDNSServer оставляет своему резолверу, заданному именем, поле
// domain_resolver: dns-direct — то есть имя провайдера уходит системному
// резолверу обычным запросом на 53-й порт. Ровно такой запрос провайдеры и
// перехватывают, и это последнее место на пути подключения, где оно ещё
// оставалось: у встроенных вариантов адрес известен заранее, а у своего — нет.
//
// Разрешаем его тем же ResolveHost и подставляем адрес. SNI при этом уже стоит
// правильный — remoteDNSServer записал туда имя, — поэтому сертификат
// проверяется как прежде, а искать резолвер через DNS больше не нужно.
//
// Делается здесь, а не в remoteDNSServer, потому что тот вызывается ещё и из
// ValidateDNSSetting — на каждую правку поля в настройках. Сетевой запрос на
// набор текста был бы неуместен.
//
// Неудача ничего не ломает: остаётся прежняя пара «имя + domain_resolver», то
// есть ровно то поведение, что было до этой правки.
func resolveRemoteDNSHost(ctx context.Context, remote map[string]interface{}) {
	if remote["domain_resolver"] == nil {
		return
	}
	host, _ := remote["server"].(string)
	ips, err := ResolveHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			remote["server"] = ip4.String()
			delete(remote, "domain_resolver")
			return
		}
	}
	// Fallback to IPv6 if no IPv4 is resolved
	if len(ips) > 0 {
		remote["server"] = ips[0].String()
		delete(remote, "domain_resolver")
		return
	}
}

// Settings represents the NeoBox application settings.
type Settings struct {
	TunMode              bool     `json:"tunMode"`
	FakeDns              bool     `json:"fakeDns"`
	Dns                  string   `json:"dns"`
	ProcessMode          string   `json:"processMode"` // "blacklist" or "whitelist"
	ProcessList          []string `json:"processList"`
	ProcessListBlacklist []string `json:"processListBlacklist"`
	ProcessListWhitelist []string `json:"processListWhitelist"`
	BypassRu             bool     `json:"bypassRu"`
	BlockQuic            bool     `json:"blockQuic"`
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
	// TlsFragment enables TLS record fragmentation (Anti-DPI) to bypass censorship/TSPU.
	// When nil, falls back to link query parameter (fragment=1/record_fragment=1).
	TlsFragment *bool `json:"tlsFragment"`
	// AllowLan binds the mixed inbound (SOCKS5+HTTP) to 0.0.0.0 instead of 127.0.0.1,
	// allowing other devices on the local area network to use this machine as a proxy.
	AllowLan bool `json:"allowLan"`
	// BlockTorrent drops BitTorrent/P2P protocol traffic to protect against host bans.
	BlockTorrent bool `json:"blockTorrent"`
	// AllowInsecureHttp allows connecting to unencrypted HTTP proxy nodes and fetching HTTP subscriptions.
	AllowInsecureHttp bool `json:"allowInsecureHttp"`
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

var base64Encodings = []*base64.Encoding{
	base64.StdEncoding,
	base64.URLEncoding,
	base64.RawStdEncoding,
	base64.RawURLEncoding,
}

// decodeBase64Any decodes a base64 payload written in any of the four common
// spellings: the standard or the URL-safe alphabet, padded or not. Clients
// disagree on which they emit and a subscription is not worth losing over it.
func decodeBase64Any(payload string) ([]byte, error) {
	clean := strings.TrimSpace(payload)
	for _, enc := range base64Encodings {
		if decoded, err := enc.DecodeString(clean); err == nil {
			return decoded, nil
		}
	}
	if rem := len(clean) % 4; rem != 0 {
		padded := clean + strings.Repeat("=", 4-rem)
		if decoded, err := base64.StdEncoding.DecodeString(padded); err == nil {
			return decoded, nil
		}
		if decoded, err := base64.URLEncoding.DecodeString(padded); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("illegal base64 data")
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

func isFragmentRequested(params url.Values) bool {
	for _, key := range []string{"fragment", "record_fragment", "anti-dpi", "antidpi"} {
		v := strings.ToLower(params.Get(key))
		if v == "1" || v == "true" || v == "yes" {
			return true
		}
	}
	return false
}

func isFragmentRequestedVMess(data map[string]interface{}) bool {
	for _, key := range []string{"fragment", "record_fragment", "anti-dpi", "antidpi"} {
		if v, ok := data[key]; ok {
			switch val := v.(type) {
			case bool:
				if val {
					return true
				}
			case string:
				vStr := strings.ToLower(val)
				if vStr == "1" || vStr == "true" || vStr == "yes" {
					return true
				}
			case int:
				if val == 1 {
					return true
				}
			case float64:
				if val == 1 {
					return true
				}
			}
		}
	}
	return false
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
		if u.Hostname() == "" {
			return nil, fmt.Errorf("invalid %s link: missing server address", protocol)
		}
		if u.User == nil || u.User.Username() == "" {
			return nil, fmt.Errorf("invalid %s link: missing user credentials", protocol)
		}
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
			if strings.EqualFold(params.Get("support-x25519mlkem768"), "true") || params.Get("support-x25519mlkem768") == "1" {
				if security == "tls" {
					tlsMap["curve_preferences"] = []string{"X25519MLKEM768", "X25519", "P256"}
				}
			}
			if isFragmentRequested(params) {
				tlsMap["record_fragment"] = true
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
			vmessTLS := map[string]interface{}{
				"enabled":     true,
				"server_name": sniVal,
				"utls": map[string]interface{}{
					"enabled":     true,
					"fingerprint": fpVal,
				},
			}
			if isFragmentRequestedVMess(vmessData) {
				vmessTLS["record_fragment"] = true
			}
			outbound["tls"] = vmessTLS
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
		if u.Hostname() == "" {
			return nil, errors.New("invalid tuic link: missing server address")
		}
		if u.User == nil || u.User.Username() == "" {
			return nil, errors.New("invalid tuic link: missing user credentials")
		}
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt

		outbound["uuid"] = u.User.Username()
		p, _ := u.User.Password()
		outbound["password"] = p

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
		if u.Hostname() == "" {
			return nil, errors.New("invalid hysteria2 link: missing server address")
		}
		if u.User == nil || u.User.Username() == "" {
			return nil, errors.New("invalid hysteria2 link: missing auth password")
		}
		portInt, _ := strconv.Atoi(u.Port())
		if portInt == 0 {
			portInt = 443
		}
		outbound["type"] = "hysteria2"
		outbound["server"] = u.Hostname()
		outbound["server_port"] = portInt
		outbound["password"] = u.User.Username()

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
		if u.Hostname() == "" {
			return nil, errors.New("invalid wireguard link: missing server address")
		}
		if u.User == nil || u.User.Username() == "" {
			return nil, errors.New("invalid wireguard link: missing private key")
		}
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
// OutboundSelectorTag is the well-known tag for the main proxy selector outbound.
const OutboundSelectorTag = "proxy-out"

// OutboundAutoTag is the well-known tag for the automatic urltest failover group.
const OutboundAutoTag = "auto-out"

// AutoServerLink is the virtual pseudo-link representing automatic urltest failover mode.
const AutoServerLink = "neobox://auto"

// GenerateConfig generates a sing-box configuration for a single proxy outbound.
// Preserved for backward compatibility.
func GenerateConfig(outbound map[string]interface{}, settings Settings, useSystemProxy bool, cacheDBPath string, clashSecret string) (map[string]interface{}, error) {
	return GenerateConfigWithCandidates(nil, outbound, settings, useSystemProxy, cacheDBPath, clashSecret)
}

// GenerateConfigWithCandidates generates a sing-box configuration containing multiple candidates
// in a "selector" outbound group, allowing zero-downtime hot-switching between servers via the Clash API.
//
// When candidates contains 2 or more outbounds and activeOutbound is not WireGuard, a "selector"
// outbound (OutboundSelectorTag) is created as route.final with all candidate tags.
// When candidates has 1 or fewer outbounds, or activeOutbound is WireGuard, a direct single-outbound
// layout is produced for complete backward compatibility.
func GenerateConfigWithCandidates(candidates []map[string]interface{}, outbound map[string]interface{}, settings Settings, useSystemProxy bool, cacheDBPath string, clashSecret string) (map[string]interface{}, error) {
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
	selectorDefaultOverride, _ := workOutbound["_selector_default"].(string)
	delete(workOutbound, "_selector_default")

	activeType, _ := workOutbound["type"].(string)
	useSelector := len(candidates) > 1 && activeType != "wireguard"
	targetDetourTag := outboundTag
	if useSelector {
		targetDetourTag = OutboundSelectorTag
	}

	// Один срок на оба разрешения имени ниже — резолвера и самого сервера.
	//
	// По отдельному сроку на каждое они складывались бы: на мёртвой сети
	// пользователь ждал бы вдвое дольше, глядя на «Подключение...».
	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer resolveCancel()

	// 1. DNS Section (Nuclear Strategy: No local DNS, only IP-based DoH for remote, local for direct)
	remote, err := remoteDNSServer(settings.Dns, targetDetourTag)
	if err != nil {
		return nil, err
	}
	resolveRemoteDNSHost(resolveCtx, remote)
	dnsServers := []map[string]interface{}{
		remote,
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

	allServerDomains := make(map[string]bool)
	allDirectIPs := make(map[string]bool)

	// Pre-resolve proxy domain to IP to avoid DNS inside the tunnel.
	// Uses a 2-second timeout so slow DNS won't freeze VPN startup.
	//
	// A WireGuard endpoint keeps its server address inside the first peer
	// instead of a top-level field, and that peer is a nested map the shallow
	// copy above still shares with the caller — so it is copied before being
	// rewritten, or resolving would reach back into the caller's outbound.
	origDomain, _ := ServerEndpoint(workOutbound)
	origDomain = strings.Trim(origDomain, "[]")
	serverDomain := origDomain
	workOutbound["domain_resolver"] = "dns-direct"
	if origDomain != "" && net.ParseIP(origDomain) == nil {
		allServerDomains[origDomain] = true
	}

	// Apply TLS record fragmentation (Anti-DPI) setting
	if tlsMap, ok := workOutbound["tls"].(map[string]interface{}); ok {
		workTLS := make(map[string]interface{}, len(tlsMap)+1)
		for k, v := range tlsMap {
			workTLS[k] = v
		}
		if settings.TlsFragment != nil {
			if *settings.TlsFragment {
				workTLS["record_fragment"] = true
			} else {
				delete(workTLS, "record_fragment")
			}
		}
		workOutbound["tls"] = workTLS
	}
	wgPeer := copyFirstWireGuardPeer(workOutbound)

	// Collect all unique domains across active outbound and candidates to resolve concurrently
	var domainsToResolve []string
	seenDomains := make(map[string]bool)
	if serverDomain != "" && net.ParseIP(serverDomain) == nil {
		domainsToResolve = append(domainsToResolve, serverDomain)
		seenDomains[serverDomain] = true
	}
	if useSelector {
		for _, cand := range candidates {
			if cand == nil {
				continue
			}
			d, _ := ServerEndpoint(cand)
			d = strings.Trim(d, "[]")
			if d != "" && net.ParseIP(d) == nil && !seenDomains[d] {
				domainsToResolve = append(domainsToResolve, d)
				seenDomains[d] = true
			}
		}
	}

	resolvedIPs := make(map[string][]net.IP, len(domainsToResolve))
	if len(domainsToResolve) > 0 {
		var resolveMu sync.Mutex
		var wg sync.WaitGroup
		for _, domain := range domainsToResolve {
			wg.Add(1)
			go func(dom string) {
				defer wg.Done()
				// Через ResolveHost, а не через системный резолвер: обычный DNS на пути к
				// серверу перехватывается провайдером, и подменённый адрес выглядел бы
				// потом как «сервер не отвечает». За ResolveHost теперь два DoH-резолвера
				// разом и системный как откат, если молчат оба.
				ips, err := ResolveHost(resolveCtx, dom)
				if err == nil && len(ips) > 0 {
					resolveMu.Lock()
					resolvedIPs[dom] = ips
					resolveMu.Unlock()
				}
			}(domain)
		}
		wg.Wait()
	}

	pickBestIP := func(ips []net.IP) string {
		for _, ip := range ips {
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
		if len(ips) > 0 {
			return ips[0].String()
		}
		return ""
	}

	if ips, ok := resolvedIPs[serverDomain]; ok {
		if best := pickBestIP(ips); best != "" {
			if wgPeer != nil {
				wgPeer["address"] = best
			} else {
				workOutbound["server"] = best
			}
		}
	}
	serverHost, _ := ServerEndpoint(workOutbound)
	cleanHost := strings.Trim(serverHost, "[]")
	if net.ParseIP(cleanHost) != nil {
		allDirectIPs[cleanHost] = true
	}

	var preparedCandidates []map[string]interface{}
	var candidateTags []string
	usedTags := make(map[string]bool)
	preparedCandidates = append(preparedCandidates, workOutbound)
	candidateTags = append(candidateTags, outboundTag)
	usedTags[outboundTag] = true

	if useSelector {
		for i, cand := range candidates {
			if cand == nil {
				continue
			}
			cType, _ := cand["type"].(string)
			if cType == "wireguard" {
				continue
			}
			cTag, _ := cand["tag"].(string)
			if cTag == "" {
				cTag = fmt.Sprintf("proxy-%d", i+1)
			}
			if cTag == outboundTag && i == 0 {
				continue
			}
			uniqueTag := cTag
			counter := 2
			for usedTags[uniqueTag] {
				uniqueTag = fmt.Sprintf("%s-%d", cTag, counter)
				counter++
			}
			usedTags[uniqueTag] = true

			wCand := make(map[string]interface{}, len(cand))
			for k, v := range cand {
				wCand[k] = v
			}
			wCand["tag"] = uniqueTag
			wCand["domain_resolver"] = "dns-direct"

			if tlsMap, ok := wCand["tls"].(map[string]interface{}); ok {
				workTLS := make(map[string]interface{}, len(tlsMap)+1)
				for k, v := range tlsMap {
					workTLS[k] = v
				}
				if settings.TlsFragment != nil {
					if *settings.TlsFragment {
						workTLS["record_fragment"] = true
					} else {
						delete(workTLS, "record_fragment")
					}
				}
				wCand["tls"] = workTLS
			}

			cDomain, _ := ServerEndpoint(wCand)
			cDomain = strings.Trim(cDomain, "[]")
			if cDomain != "" && net.ParseIP(cDomain) == nil {
				allServerDomains[cDomain] = true
				if ips, ok := resolvedIPs[cDomain]; ok {
					if best := pickBestIP(ips); best != "" {
						wCand["server"] = best
					}
				}
			}
			cH, _ := ServerEndpoint(wCand)
			cCleanH := strings.Trim(cH, "[]")
			if net.ParseIP(cCleanH) != nil {
				allDirectIPs[cCleanH] = true
			}

			preparedCandidates = append(preparedCandidates, wCand)
			candidateTags = append(candidateTags, uniqueTag)
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

	// Exclude proxy server IP and/or domain from proxy tunnel so the transport
	// connection never gets routed back into the tunnel itself.
	// This MUST precede the IPv6 reject rule, otherwise an IPv6 proxy server would
	// be matched and dropped by "ip_version: 6 reject"!
	// Uses ServerEndpoint to properly extract the endpoint for all protocols,
	// including WireGuard which stores its address in peers[0]["address"].
	directIPList := make([]string, 0, len(allDirectIPs))
	for ipStr := range allDirectIPs {
		directIPList = append(directIPList, ipStr)
	}
	sort.Strings(directIPList)
	for _, ipStr := range directIPList {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		cidr := ipStr + "/32"
		if ip.To4() == nil {
			cidr = ipStr + "/128"
		} else {
			cidr = ip.To4().String() + "/32"
		}
		routeRules = append(routeRules, map[string]interface{}{
			"ip_cidr":  []string{cidr},
			"action":   "route",
			"outbound": "direct",
		})
	}
	serverDomainList := make([]string, 0, len(allServerDomains))
	for d := range allServerDomains {
		serverDomainList = append(serverDomainList, d)
	}
	sort.Strings(serverDomainList)
	for _, d := range serverDomainList {
		routeRules = append(routeRules, map[string]interface{}{
			"domain":   []string{d},
			"action":   "route",
			"outbound": "direct",
		})
	}

	// Block IPv6 to prevent leaks (only applied to non-exempt traffic).
	// Default: true (protected). Only skip if user explicitly set ipv6Leak=false.
	if boolDefault(settings.Ipv6LeakProtection, true) {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_version": 6, "action": "reject",
		})
	}

	// Block BitTorrent (P2P) in the tunnel to prevent host bans / DMCA complaints.
	if settings.BlockTorrent {
		routeRules = append(routeRules, map[string]interface{}{
			"protocol": []string{"bittorrent"},
			"action":   "reject",
			"method":   "default",
		})
	}

	// Block QUIC / HTTP-3 (UDP 443) traffic, forcing clients to fallback to TCP (HTTP/1.1 and HTTP/2),
	// which prevents throttling and censorship of YouTube/Google services on UDP.
	if settings.BlockQuic {
		routeRules = append(routeRules, map[string]interface{}{
			"network": "udp",
			"port":    []int{443},
			"action":  "reject",
			"method":  "default",
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
			outbound = targetDetourTag
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
		case "process":
			if !tunMode {
				continue
			}
			name, ok := NormaliseProcessName(rule.Value)
			if !ok {
				continue
			}
			ruleEntry["process_name"] = []string{name}
		default:
			continue
		}
		routeRules = append(routeRules, ruleEntry)
	}

	// Process routing (split tunneling) and exclusion rules order:
	// 1. Blacklist process mode routes specified processes direct immediately.
	// 2. Local & private IPs (ip_is_private: true) route direct BEFORE whitelist
	//    so LAN/gateway remain reachable.
	// 3. Bypass Russia (rule-sets geoip-ru / geosite-category-ru) routes direct.
	// 4. Whitelist process mode routes named processes to the VPN outbound (targetDetourTag).
	// 5. FakeIP CIDR rule directs synthetic 198.18.0.0/15 addresses into the tunnel.
	// 6. Whitelist catch-all routes all remaining traffic direct.

	if tunMode && len(processList) > 0 && processMode == "blacklist" {
		routeRules = append(routeRules, map[string]interface{}{
			"process_name": processList,
			"action":       "route",
			"outbound":     "direct",
		})
	}

	routeRules = append(routeRules, map[string]interface{}{
		"ip_is_private": true,
		"action":        "route",
		"outbound":      "direct",
	})

	// Bypass Russia (rule-sets)
	if settings.BypassRu {
		routeRules = append(routeRules, map[string]interface{}{
			"rule_set": []string{"geoip-ru", "geosite-category-ru"},
			"action":   "route",
			"outbound": "direct",
		})
	}

	whitelistCatchAll := false
	if tunMode && len(processList) > 0 && processMode != "blacklist" {
		routeRules = append(routeRules, map[string]interface{}{
			"process_name": processList,
			"action":       "route",
			"outbound":     targetDetourTag,
		})
		whitelistCatchAll = true
	}

	// FakeIP Cidr rule (must be placed AFTER domain and process exclusions!)
	if settings.FakeDns && tunMode {
		routeRules = append(routeRules, map[string]interface{}{
			"ip_cidr":  []string{"198.18.0.0/15"},
			"action":   "route",
			"outbound": targetDetourTag,
		})
	}

	// Whitelist split tunneling: everything not named in the process list goes
	// out untunnelled. Deliberately last — see whitelistCatchAll above.
	if whitelistCatchAll {
		routeRules = append(routeRules, map[string]interface{}{
			"action":   "route",
			"outbound": "direct",
		})
	}

	// 4. Final Config Structure
	domains := []string{"localhost", "wails.localhost"}
	seenDNSEntry := map[string]bool{"localhost": true, "wails.localhost": true}
	for _, d := range serverDomainList {
		if !seenDNSEntry[d] {
			seenDNSEntry[d] = true
			domains = append(domains, d)
		}
	}
	firstDNSRule := map[string]interface{}{
		"domain_suffix": []string{".local", ".localhost"},
		"domain":        domains,
		"server":        "dns-direct",
	}
	dnsRules := []map[string]interface{}{firstDNSRule}
	if settings.BypassRu {
		// RU names are resolved outside the tunnel, because their traffic leaves
		// outside it: an address chosen by the remote resolver points at the
		// CDN edge nearest the exit node, not the one nearest the user. Under
		// FakeDNS it is worse than slow -- the answer is a 198.18/15 address, so
		// the geoip-ru half of the route rule has no real IP left to match.
		dnsRules = append(dnsRules, map[string]interface{}{
			"rule_set": []string{"geosite-category-ru"},
			"action":   "route",
			"server":   "dns-direct",
		})
	}
	if settings.FakeDns && tunMode {
		dnsRules = append(dnsRules, map[string]interface{}{
			"query_type": []string{"A", "AAAA"},
			"action":     "route",
			"server":     "dns-fake",
		})
	} else if boolDefault(settings.Ipv6LeakProtection, true) {
		// When IPv6 leak protection is on and fakeip is not used, route AAAA queries
		// through dns-remote so they cannot escape the tunnel into local network.
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

	var outboundsList []interface{}
	if useSelector {
		selectorOutbounds := make([]string, 0, len(candidateTags)+1)
		selectorOutbounds = append(selectorOutbounds, OutboundAutoTag)
		selectorOutbounds = append(selectorOutbounds, candidateTags...)

		selectorDefault := outboundTag
		if selectorDefaultOverride != "" {
			selectorDefault = selectorDefaultOverride
		}

		selectorOutbound := map[string]interface{}{
			"type":                        "selector",
			"tag":                         OutboundSelectorTag,
			"outbounds":                   selectorOutbounds,
			"default":                     selectorDefault,
			"interrupt_exist_connections": true,
		}

		autoOutbound := map[string]interface{}{
			"type":                        "urltest",
			"tag":                         OutboundAutoTag,
			"outbounds":                   candidateTags,
			"url":                         "https://www.gstatic.com/generate_204",
			"interval":                    "3m",
			"tolerance":                   50,
			"idle_timeout":                "30m",
			"interrupt_exist_connections": true,
		}

		outboundsList = append(outboundsList, selectorOutbound, autoOutbound)
		for _, cand := range preparedCandidates {
			outboundsList = append(outboundsList, cand)
		}
	} else {
		outboundsList = append(outboundsList, workOutbound)
	}
	outboundsList = append(outboundsList,
		map[string]interface{}{"type": "direct", "tag": "direct"},
		map[string]interface{}{"type": "block", "tag": "block"},
	)

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
			"optimistic":      true,
		},
		"inbounds": []map[string]interface{}{
			{
				"type": "mixed",
				"tag":  "mixed-in",
				"listen": func() string {
					if settings.AllowLan {
						return "0.0.0.0"
					}
					return ProxyListenHost
				}(),
				"listen_port":      ProxyListenPort,
				"set_system_proxy": useSystemProxy,
			},
		},
		"outbounds": outboundsList,
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
			"final": targetDetourTag,
		},
		"experimental": map[string]interface{}{
			"cache_file": map[string]interface{}{
				"enabled":      true,
				"path":         cacheDBPath,
				"store_fakeip": true,
				"store_dns":    true,
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
		downloadDetour := targetDetourTag
		if outboundType, _ := workOutbound["type"].(string); outboundType == "wireguard" {
			downloadDetour = "direct"
		}
		// Both lists are fetched through the tunnel, not around it. They live on
		// raw.githubusercontent.com, which is exactly the kind of host this
		// feature exists to reach: a "direct" detour meant the download failed
		// for exactly the users who turn the bypass on. And a failed download is
		// not a rule that quietly never matches: with no cached copy, sing-box
		// aborts the whole start over it (route/rule/rule_set_remote.go,
		// "initial rule-set: ..."), so the toggle took the core down with it.
		//
		// The domain list is "geosite-category-ru": sing-geosite has no
		// "geosite-ru.srs", so that URL answered 404 and only the geoip half of
		// the bypass ever had a chance of loading.
		// Configure top-level HTTP client and route default client (sing-box 1.14+).
		config["http_clients"] = []map[string]interface{}{
			{
				"tag":    "ruleset-client",
				"detour": downloadDetour,
			},
		}
		routeSection["default_http_client"] = "ruleset-client"

		rulesDir := filepath.Join(filepath.Dir(cacheDBPath), "rules")
		initialGeoIP := filepath.Join(rulesDir, "geoip-ru.srs")
		initialGeosite := filepath.Join(rulesDir, "geosite-category-ru.srs")

		routeSection["rule_set"] = []map[string]interface{}{
			{
				"tag":             "geoip-ru",
				"type":            "remote",
				"format":          "binary",
				"url":             "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-ru.srs",
				"initial_path":    initialGeoIP,
				"http_client":     "ruleset-client",
				"download_detour": downloadDetour,
			},
			{
				"tag":             "geosite-category-ru",
				"type":            "remote",
				"format":          "binary",
				"url":             "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ru.srs",
				"initial_path":    initialGeosite,
				"http_client":     "ruleset-client",
				"download_detour": downloadDetour,
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
			"udp_mapping":    "endpoint_independent",
			"udp_filtering":  "endpoint_independent",
			"udp_timeout":    "5m",
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

// [INTERNET SEARCH PATTERN]
// Safe SSRF prevention: Comprehensive IP validation against private, loopback,
// link-local, carrier-grade NAT, cloud metadata, and reserved networks.
// Based on standard Go security practices (RFC 1918, RFC 3927, RFC 4193, RFC 6598, RFC 4291).
var restrictedCIDRs = []string{
	"0.0.0.0/8",          // Current network (RFC 1122)
	"10.0.0.0/8",         // Private network (RFC 1918)
	"100.64.0.0/10",      // Shared Address Space / CGNAT (RFC 6598)
	"127.0.0.0/8",        // Loopback (RFC 1122)
	"169.254.0.0/16",     // Link-local / Cloud Metadata: AWS, GCP, Azure, DO (RFC 3927)
	"172.16.0.0/12",      // Private network (RFC 1918)
	"192.0.0.0/24",       // IETF Protocol Assignments (RFC 6890)
	"192.0.2.0/24",       // Documentation / TEST-NET-1 (RFC 5737)
	"192.168.0.0/16",     // Private network (RFC 1918)
	"198.18.0.0/15",      // Network Interconnect Benchmark Testing / FakeIP (RFC 2544)
	"198.51.100.0/24",    // Documentation / TEST-NET-2 (RFC 5737)
	"203.0.113.0/24",     // Documentation / TEST-NET-3 (RFC 5737)
	"224.0.0.0/4",        // Multicast (RFC 5771)
	"240.0.0.0/4",        // Reserved for future use (RFC 1112)
	"255.255.255.255/32", // Limited Broadcast (RFC 919)
	"::/96",              // IPv4-compatible IPv6 (deprecated RFC 4291)
	"::/128",             // Unspecified IPv6
	"::1/128",            // Loopback IPv6 (RFC 4291)
	"fc00::/7",           // Unique Local Addresses / ULA (RFC 4193)
	"fe80::/10",          // Link-Local Unicast (RFC 4291)
	"ff00::/8",           // Multicast (RFC 4291)
	"64:ff9b::/96",       // IPv4/IPv6 translation (RFC 6052)
	"100::/64",           // Discard prefix (RFC 6666)
	"2001::/32",          // Teredo tunneling prefix (RFC 4380)
	"2001:db8::/32",      // Documentation (RFC 3849)
	"2002::/16",          // 6to4 prefix (RFC 3056)
	"fec0::/10",          // Site-Local Unicast (deprecated RFC 3879)
	"192.175.48.0/24",    // AS112 direct delegation (RFC 7535)
}

var restrictedPrefixes []netip.Prefix

func init() {
	for _, cidr := range restrictedCIDRs {
		if prefix, err := netip.ParsePrefix(cidr); err == nil {
			restrictedPrefixes = append(restrictedPrefixes, prefix)
		}
	}
}

// IsPublicAddr reports whether the given netip.Addr is a publicly routable address.
// Zero-allocation check against loopback, private, link-local, carrier-grade NAT,
// cloud metadata, and reserved networks.
func IsPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()

	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}

	for _, prefix := range restrictedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// IsPublicIP preserves backward compatibility for net.IP callers.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok || !addr.IsValid() {
		return false
	}
	return IsPublicAddr(addr)
}

// isRestrictedHost checks if a hostname or literal IP target should be rejected
// up front to prevent SSRF against internal resources or local infrastructure.
func isRestrictedHost(rawHost string) bool {
	host := strings.TrimSpace(strings.ToLower(rawHost))
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "" {
		return true
	}
	if host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".onion") {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return !IsPublicAddr(addr)
	}
	return false
}

type allowInsecureHTTPContextKey struct{}

// WithAllowInsecureHTTP attaches a permission flag to the context to allow plain HTTP subscriptions.
func WithAllowInsecureHTTP(ctx context.Context) context.Context {
	return context.WithValue(ctx, allowInsecureHTTPContextKey{}, true)
}

func isAllowInsecureHTTP(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(allowInsecureHTTPContextKey{}).(bool)
	return v
}

// refuseInsecureRedirect keeps a subscription fetch on HTTPS for the whole of
// its redirect chain and validates target hosts against SSRF attacks.
//
// Rejecting an "http://" URL up front only covers the address the user typed.
// Go's client follows up to ten redirects on its own, so a host answering an
// HTTPS request with "302 Location: http://…" got the body fetched in plain
// text anyway — the exact interception the scheme check exists to prevent, one
// header away. The subscription body is a list of proxy servers, so an attacker
// who can rewrite it chooses which server the user connects through.
func refuseInsecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !strings.EqualFold(req.URL.Scheme, "https") && !(strings.EqualFold(req.URL.Scheme, "http") && isAllowInsecureHTTP(req.Context())) {
		return fmt.Errorf("refusing redirect to %s: subscriptions must stay on HTTPS", req.URL.Scheme)
	}
	hostname := strings.Trim(strings.TrimSpace(req.URL.Hostname()), "[]")
	if isRestrictedHost(hostname) {
		return fmt.Errorf("refusing redirect to restricted host %q: SSRF protection", hostname)
	}
	if addr, err := netip.ParseAddr(hostname); err == nil {
		if !IsPublicAddr(addr) {
			return fmt.Errorf("refusing redirect to private IP %s: SSRF protection", addr.String())
		}
	}
	return nil
}

// errSubBlocked marks a response that arrived intact but carried no
// subscription: an HTML page or an empty body. The failure is content-level
// rather than transport-level, which makes it the only one worth retrying with
// a different User-Agent.
var errSubBlocked = errors.New("returned HTML or empty data")

// SubscriptionUserInfo holds quota and expiration metadata extracted from the
// "Subscription-Userinfo" HTTP response header supplied by proxy panel providers.
type SubscriptionUserInfo struct {
	Upload   int64 `json:"upload"`   // bytes uploaded
	Download int64 `json:"download"` // bytes downloaded
	Total    int64 `json:"total"`    // total quota in bytes (0 = unlimited / unspecified)
	Expire   int64 `json:"expire"`   // unix timestamp in seconds when subscription expires (0 = no expiration)
}

// SubscriptionPayload contains parsed server links alongside subscription quota metadata.
type SubscriptionPayload struct {
	Links    []string              `json:"links"`
	UserInfo *SubscriptionUserInfo `json:"userInfo,omitempty"`
}

// ParseSubscriptionUserInfo parses a "Subscription-Userinfo" header string
// (e.g. "upload=1073741824; download=10737418240; total=107374182400; expire=1735689600").
// It handles case-insensitivity, arbitrary spacing, and missing fields cleanly.
// It returns nil if the header string contains no recognizable userinfo fields.
func ParseSubscriptionUserInfo(header string) *SubscriptionUserInfo {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return nil
	}

	info := &SubscriptionUserInfo{}
	foundAny := false

	parts := strings.Split(trimmed, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		valStr := strings.TrimSpace(kv[1])
		val, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil || val < 0 {
			continue
		}

		switch key {
		case "upload":
			info.Upload = val
			foundAny = true
		case "download":
			info.Download = val
			foundAny = true
		case "total":
			info.Total = val
			foundAny = true
		case "expire":
			info.Expire = val
			foundAny = true
		}
	}

	if !foundAny {
		return nil
	}
	return info
}

// fetchSubscriptionBodyWithInfo performs a single GET and returns the body and parsed
// Subscription-Userinfo only when the response looks like subscription data.
func fetchSubscriptionBodyWithInfo(ctx context.Context, client *http.Client, subURL, userAgent string) ([]byte, *SubscriptionUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var userInfo *SubscriptionUserInfo
	if rawInfo := resp.Header.Get("Subscription-Userinfo"); rawInfo != "" {
		userInfo = ParseSubscriptionUserInfo(rawInfo)
	}

	// LimitReader prevents unbounded memory exhaustion from malicious subscription endpoints (max 20MB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("response read error: %w", err)
	}

	// Sniff first 1024 bytes without allocating the entire 20MB body into a new lowercased string
	sniffLen := len(body)
	if sniffLen > 1024 {
		sniffLen = 1024
	}
	sniffHeader := bytes.ToLower(bytes.TrimSpace(body[:sniffLen]))
	if len(sniffHeader) == 0 ||
		bytes.Contains(sniffHeader, []byte("<html")) ||
		bytes.Contains(sniffHeader, []byte("<!doctype html")) ||
		bytes.Contains(sniffHeader, []byte("<script")) {
		return nil, nil, errSubBlocked
	}
	return body, userInfo, nil
}

// fetchSubscriptionBody performs a single GET and returns the body only when it
// looks like subscription data.
func fetchSubscriptionBody(ctx context.Context, client *http.Client, subURL, userAgent string) ([]byte, error) {
	body, _, err := fetchSubscriptionBodyWithInfo(ctx, client, subURL, userAgent)
	return body, err
}

// FetchSubscription loads subscription contents using background context.
// Provided for backward compatibility.
func FetchSubscription(subURL string) ([]string, error) {
	return FetchSubscriptionContext(context.Background(), subURL)
}

// FetchSubscriptionContext loads subscription contents (both JSON-based xray-ext and standard lists)
// with context cancellation support and hardened SSRF protection.
func FetchSubscriptionContext(ctx context.Context, subURL string) ([]string, error) {
	payload, err := FetchSubscriptionPayloadContext(ctx, subURL)
	if err != nil {
		return nil, err
	}
	return payload.Links, nil
}

// FetchSubscriptionPayloadContext loads subscription contents and extracts Subscription-Userinfo
// with context cancellation support and hardened SSRF protection.
//
// Security:
// - HTTP (non-TLS) subscription URLs are rejected to prevent MITM attacks.
// - Private, loopback, link-local, and cloud metadata destinations (e.g. 169.254.169.254) are rejected (SSRF prevention).
// - DNS rebinding attacks are prevented via net.Dialer.Control IP validation before TCP connection.
// - URLs longer than maxLinkLength are rejected before the request is made.
func FetchSubscriptionPayloadContext(ctx context.Context, subURL string) (*SubscriptionPayload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	trimmedURL := strings.TrimSpace(subURL)

	if len(trimmedURL) > maxLinkLength {
		return nil, errors.New(i18n.T(i18n.ErrSubURLTooLong, maxLinkLength))
	}

	trimmedURL = strings.ReplaceAll(trimmedURL, " ", "%20")
	trimmedURL = strings.ReplaceAll(trimmedURL, "\t", "%09")
	lowerURL := strings.ToLower(trimmedURL)

	// Direct proxy links (vless://, vmess://, etc.) bypass HTTP fetching.
	if IsProxyLink(trimmedURL) {
		return &SubscriptionPayload{Links: []string{trimmedURL}}, nil
	}

	// Security: block plain HTTP subscription URLs unless explicitly allowed by user.
	if strings.HasPrefix(lowerURL, "http://") && !isAllowInsecureHTTP(ctx) {
		return nil, errors.New(i18n.T(i18n.ErrSubURLNotSecure))
	}

	parsedURL, err := url.Parse(trimmedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid subscription URL: %w", err)
	}

	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "https" && !(scheme == "http" && isAllowInsecureHTTP(ctx)) {
		return nil, errors.New(i18n.T(i18n.ErrSubURLNotSecure))
	}

	// Validate target hostname and IP up front against SSRF and cloud metadata addresses
	destHost := strings.Trim(strings.TrimSpace(parsedURL.Hostname()), "[]")
	if isRestrictedHost(destHost) {
		return nil, fmt.Errorf("subscription URL points to a private or restricted host %q: SSRF protection", destHost)
	}

	// Pre-resolve host via DoH / bootstrap DNS to detect DNS rebinding before any transport connects
	if addr, err := netip.ParseAddr(destHost); err == nil {
		if !IsPublicAddr(addr) {
			return nil, fmt.Errorf("subscription URL points to a private IP %s: SSRF protection", addr.String())
		}
	} else if ips, err := ResolveHost(ctx, destHost); err == nil && len(ips) > 0 {
		for _, ip := range ips {
			if !IsPublicIP(ip) {
				return nil, fmt.Errorf("subscription host %q resolved to private or restricted IP %s: SSRF protection", destHost, ip.String())
			}
		}
	}

	// DNS rebinding and SSRF protection via net.Dialer.Control hook.
	// Executes after DNS resolution and socket creation, but before TCP handshake (connect(2)).
	// Validates that the resolved socket address is not a private or restricted IP.
	safeDialControl := func(network, address string, c syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(host), "[]"))
		if err != nil {
			return fmt.Errorf("ssrf guard: unable to parse resolved IP %q: %w", host, err)
		}
		if !IsPublicAddr(addr) {
			return fmt.Errorf("ssrf guard: connection to private or restricted IP %s blocked", addr.String())
		}
		return nil
	}

	safeDialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: safeDialControl,
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
			Timeout:       10 * time.Second,
			CheckRedirect: refuseInsecureRedirect,
			Transport: &http.Transport{
				Proxy:               http.ProxyURL(proxyURL),
				TLSClientConfig:     SecureTLSConfig(""),
				TLSHandshakeTimeout: 10 * time.Second,
			},
		}})
	}
	transports = append(transports, transport{"direct", &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: refuseInsecureRedirect,
		Transport: &http.Transport{
			DialContext:         safeDialer.DialContext,
			TLSClientConfig:     SecureTLSConfig(""),
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}})
	defer func() {
		for _, t := range transports {
			if tr, ok := t.client.Transport.(*http.Transport); ok {
				tr.CloseIdleConnections()
			}
		}
	}()

	// Each transport asks with a browser User-Agent first. Only when the host
	// answers with an HTML page or nothing at all — the signature of a panel
	// that sniffs the User-Agent — is the request repeated announcing a proxy
	// client, which is the one thing a different UA can fix.
	userAgents := []string{getRandomUserAgent(), subscriptionClientUA}

	var bodyBytes []byte
	var userInfo *SubscriptionUserInfo
	var fetchErrs []string
	anyBlocked := false

fetch:
	for _, t := range transports {
		for _, ua := range userAgents {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			body, uInfo, err := fetchSubscriptionBodyWithInfo(ctx, t.client, trimmedURL, ua)
			if err == nil {
				bodyBytes = body
				userInfo = uInfo
				break fetch
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", t.name, err))
			if !errors.Is(err, errSubBlocked) {
				break // the transport itself failed; another UA will not help
			}
			anyBlocked = true
		}
	}

	if bodyBytes == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
				return &SubscriptionPayload{Links: links, UserInfo: userInfo}, nil
			}
		}
	}

	// sing-box's own format: either a whole config object with an "outbounds"
	// list, or a bare array of outbounds that the xray-ext branch above did not
	// recognise as its own.
	if strings.HasPrefix(rawData, "{") || strings.HasPrefix(rawData, "[") {
		if links := parseSingboxOutbounds(bodyBytes); len(links) > 0 {
			return &SubscriptionPayload{Links: links, UserInfo: userInfo}, nil
		}
	}

	// Clash YAML, as served by most panels and sub-converters.
	if looksLikeClashYAML(rawData) {
		if links := parseClashYAML(bodyBytes); len(links) > 0 {
			return &SubscriptionPayload{Links: links, UserInfo: userInfo}, nil
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
	return &SubscriptionPayload{Links: parsedLinks, UserInfo: userInfo}, nil
}
