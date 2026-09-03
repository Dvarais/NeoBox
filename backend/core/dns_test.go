package core

import (
	"strings"
	"testing"
)

// The DNS server the user picks must actually reach the generated config.
//
// It did not: Settings.Dns was declared, written by the UI on every save, and
// referenced nowhere in the backend. dns-remote was hardcoded to Cloudflare, so
// picking Google or Quad9 changed the dropdown and nothing else — which is
// indistinguishable, from the outside, from the setting not working at all.
func TestRemoteDNSServerHonoursTheSetting(t *testing.T) {
	cases := []struct {
		name       string
		setting    string
		wantServer string
		wantSNI    string
		wantPath   string
	}{
		{"empty falls back to the default", "", "1.1.1.1", "cloudflare-dns.com", "/dns-query"},
		{"cloudflare", "1.1.1.1", "1.1.1.1", "cloudflare-dns.com", "/dns-query"},
		{"google", "8.8.8.8", "8.8.8.8", "dns.google", "/dns-query"},
		{"quad9", "9.9.9.9", "9.9.9.9", "dns.quad9.net", "/dns-query"},
		{"custom DoH URL", "https://dns.adguard.com/dns-query", "dns.adguard.com", "dns.adguard.com", "/dns-query"},
		{"custom DoH URL with its own path", "https://doh.example/resolve", "doh.example", "doh.example", "/resolve"},
		{"custom URL without a path", "https://doh.example", "doh.example", "doh.example", "/dns-query"},
		{"surrounding whitespace", "  8.8.8.8  ", "8.8.8.8", "dns.google", "/dns-query"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, err := remoteDNSServer(tc.setting, "proxy")
			if err != nil {
				t.Fatalf("remoteDNSServer(%q): %v", tc.setting, err)
			}
			if got := server["server"]; got != tc.wantServer {
				t.Errorf("server = %v, want %v", got, tc.wantServer)
			}
			if got := server["path"]; got != tc.wantPath {
				t.Errorf("path = %v, want %v", got, tc.wantPath)
			}
			tls, ok := server["tls"].(map[string]interface{})
			if !ok {
				t.Fatalf("no tls block: %#v", server)
			}
			if got := tls["server_name"]; got != tc.wantSNI {
				t.Errorf("server_name = %v, want %v", got, tc.wantSNI)
			}
		})
	}
}

// Whatever the user picks, the two properties the rest of the DNS design rests
// on must hold: the query is encrypted, and it leaves through the tunnel.
func TestRemoteDNSServerStaysEncryptedAndTunnelled(t *testing.T) {
	for _, setting := range []string{"", "8.8.8.8", "https://dns.adguard.com/dns-query"} {
		server, err := remoteDNSServer(setting, "my-proxy")
		if err != nil {
			t.Fatalf("remoteDNSServer(%q): %v", setting, err)
		}
		if got := server["type"]; got != "https" {
			t.Errorf("%q: type = %v, want https — plain DNS is readable on the path", setting, got)
		}
		if got := server["detour"]; got != "my-proxy" {
			t.Errorf("%q: detour = %v, want the proxy outbound so the query stays inside the tunnel", setting, got)
		}
		tls, ok := server["tls"].(map[string]interface{})
		if !ok || tls["enabled"] != true {
			t.Errorf("%q: TLS is not enabled: %#v", setting, server)
		}
	}
}

// A resolver named by IP needs no lookup before it can be dialled. A hostname
// does, and that lookup cannot use the resolver being defined — so it is sent
// to the system one, which must be stated explicitly rather than left to
// whatever the default happens to be.
func TestRemoteDNSServerBootstrapsOnlyWhenItMust(t *testing.T) {
	byIP, err := remoteDNSServer("9.9.9.9", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := byIP["domain_resolver"]; present {
		t.Error("a resolver named by IP must not ask for a bootstrap lookup")
	}

	byName, err := remoteDNSServer("https://dns.adguard.com/dns-query", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if got := byName["domain_resolver"]; got != "dns-direct" {
		t.Errorf("domain_resolver = %v, want dns-direct so the hostname can be resolved at all", got)
	}
}

// An unusable value is reported instead of being quietly replaced by the
// default. Falling back silently is the behaviour this whole change exists to
// remove: the user would again be watching a resolver they did not choose, with
// nothing on screen to say so.
func TestRemoteDNSServerRejectsUnusableValues(t *testing.T) {
	for _, setting := range []string{
		"192.168.1.1",                  // локальный резолвер: через detour он ищется в сети сервера
		"127.0.0.1",                    // петля: то же самое
		"http://dns.example/dns-query", // plain HTTP
		"https://",                     // no host
		"https://doh.example:99999/",   // port out of range
	} {
		if _, err := remoteDNSServer(setting, "proxy"); err == nil {
			t.Errorf("remoteDNSServer(%q) accepted an unusable value", setting)
		}
	}
}

// The message has to tell the user what they can type instead, and list the
// built-in choices in a stable order.
func TestRemoteDNSServerErrorNamesTheAlternatives(t *testing.T) {
	// Раньше здесь стоял 4.4.4.4. Теперь произвольный публичный адрес
	// принимается как DoH по IP, поэтому нужен вход, который остаётся
	// непригодным при любом прочтении.
	_, err := remoteDNSServer("не адрес вовсе", "proxy")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"1.1.1.1", "8.8.8.8", "9.9.9.9", "https://"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %q: %s", want, msg)
		}
	}
}

// A custom port survives into the config, since a DoH endpoint is not obliged
// to sit on 443.
func TestRemoteDNSServerKeepsACustomPort(t *testing.T) {
	server, err := remoteDNSServer("https://doh.example:8443/dns-query", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if got := server["server_port"]; got != 8443 {
		t.Errorf("server_port = %v, want 8443", got)
	}
}

// End to end through GenerateConfig: the setting has to survive the whole way
// into the config the core is handed, not merely out of the helper.
func TestGenerateConfigUsesTheChosenDNS(t *testing.T) {
	outbound := map[string]interface{}{"type": "vless", "tag": "proxy", "server": "192.0.2.10", "server_port": 443}
	settings := Settings{Dns: "9.9.9.9"}

	cfg, err := GenerateConfig(outbound, settings, false, t.TempDir()+"/cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	dns, ok := cfg["dns"].(map[string]interface{})
	if !ok {
		t.Fatal("config has no dns section")
	}
	servers, ok := dns["servers"].([]map[string]interface{})
	if !ok {
		t.Fatal("dns section has no servers")
	}
	for _, srv := range servers {
		if srv["tag"] != "dns-remote" {
			continue
		}
		if got := srv["server"]; got != "9.9.9.9" {
			t.Errorf("dns-remote server = %v, want the chosen 9.9.9.9", got)
		}
		return
	}
	t.Error("there is no dns-remote server")
}

// And an unusable setting stops the connection with an explanation rather than
// connecting with a resolver the user did not ask for.
func TestGenerateConfigRejectsUnusableDNS(t *testing.T) {
	outbound := map[string]interface{}{"type": "vless", "tag": "proxy", "server": "192.0.2.10", "server_port": 443}
	settings := Settings{Dns: "not a dns server"}

	if _, err := GenerateConfig(outbound, settings, false, t.TempDir()+"/cache.db", "secret"); err == nil {
		t.Error("GenerateConfig accepted an unusable DNS setting")
	}
}

// Голое имя хоста принимается и достраивается до DoH.
//
// Раньше оно стояло в списке отвергаемых выше, рядом с мусором. Пользователь
// сообщил, что «свой DNS» вписать нельзя — приложение отвечало, что годятся
// только 1.1.1.1 и 8.8.8.8, — и упирался именно в это: dns.adguard-dns.com
// выглядит как самая естественная запись своего резолвера.
//
// Принцип, который защищает TestRemoteDNSServerRejectsUnusableValues, здесь не
// нарушается: молча подставляется не другой резолвер, а недостающая схема к
// тому хосту, который назвал пользователь. Поле и предназначено только для
// DoH — обычного DNS в продукте нет вовсе.
func TestRemoteDNSServerAcceptsBareHostname(t *testing.T) {
	server, err := remoteDNSServer("dns.adguard-dns.com", "proxy")
	if err != nil {
		t.Fatalf("имя хоста должно приниматься: %v", err)
	}
	if got := server["server"]; got != "dns.adguard-dns.com" {
		t.Errorf("server = %v", got)
	}
	if got := server["path"]; got != "/dns-query" {
		t.Errorf("путь по умолчанию не подставлен: %v", got)
	}
	if got := server["type"]; got != "https" {
		t.Errorf("сервер обязан остаться DoH, получено %v", got)
	}
	tls, _ := server["tls"].(map[string]interface{})
	if tls == nil || tls["server_name"] != "dns.adguard-dns.com" {
		t.Errorf("SNI должен называть тот же хост: %v", tls)
	}
	// Имя надо где-то разрешить, прежде чем к нему подключаться, — как и у
	// записи с полным URL.
	if got := server["domain_resolver"]; got != "dns-direct" {
		t.Errorf("domain_resolver = %v, ожидался dns-direct", got)
	}
}

// Сообщение об отказе должно называть годные формы записи, иначе пользователь
// остаётся ровно там же, где был.
func TestRemoteDNSServerErrorExplainsAcceptedForms(t *testing.T) {
	_, err := remoteDNSServer("не адрес вовсе", "proxy")
	if err == nil {
		t.Fatal("мусор принят, а не должен")
	}
	for _, want := range []string{"hostname", "https://", "Plain DNS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q: %v", want, err)
		}
	}
}

// У локального адреса своя причина отказа, и она обязана называть именно её:
// «дайте имя хоста» тут бесполезно, потому что дело не в форме записи, а в том,
// что через туннель этот адрес принадлежит чужой сети.
func TestRemoteDNSServerExplainsWhyLocalAddressFails(t *testing.T) {
	for _, setting := range []string{"192.168.1.1", "127.0.0.1", "10.0.0.53"} {
		_, err := remoteDNSServer(setting, "proxy")
		if err == nil {
			t.Fatalf("%s принят, хотя через туннель недостижим", setting)
		}
		if !strings.Contains(err.Error(), "tunnel") {
			t.Errorf("%s: причина не объяснена: %v", setting, err)
		}
	}
}

// Проверка для интерфейса и сборка конфига обязаны отвечать одинаково: иначе
// поле принимает значение, на котором потом падает подключение.
func TestValidateDNSSettingMatchesGeneration(t *testing.T) {
	for _, value := range []string{"", "1.1.1.1", "dns.example.com", "https://dns.example.com/dns-query"} {
		if msg := ValidateDNSSetting(value); msg != "" {
			t.Errorf("%q забраковано проверкой, хотя конфиг собирается: %s", value, msg)
		}
	}
	for _, value := range []string{"192.168.1.1", "не адрес вовсе", "ftp://dns.example.com"} {
		if ValidateDNSSetting(value) == "" {
			t.Errorf("%q прошло проверку, хотя конфиг на нём падает", value)
		}
	}
}

// Приём голого IP.
//
// Пользователь попросил принимать не только ссылки, но и адреса. Отказ раньше
// упирался в проверку сертификата: для DoH нужно имя, а у произвольного адреса
// его нет. Выход — не отправлять SNI совсем и проверять сертификат по IP-SAN,
// который крупные резолверы несут.
func TestRemoteDNSServerAcceptsBareIPWithoutSNI(t *testing.T) {
	server, err := remoteDNSServer("45.90.28.0", "proxy")
	if err != nil {
		t.Fatalf("публичный IP должен приниматься: %v", err)
	}
	if got := server["server"]; got != "45.90.28.0" {
		t.Errorf("server = %v", got)
	}
	if got := server["type"]; got != "https" {
		t.Errorf("сервер обязан остаться DoH, получено %v", got)
	}

	tls, _ := server["tls"].(map[string]interface{})
	if tls == nil {
		t.Fatal("блок tls отсутствует")
	}
	if tls["enabled"] != true {
		t.Error("проверка сертификата обязана остаться включённой")
	}
	// Главное в этом тесте. SNI по RFC 6066 — доменное имя; IP-литерал в нём
	// недопустим, и сервер такой либо игнорирует, либо рвёт рукопожатие.
	if name, present := tls["server_name"]; present {
		t.Errorf("для голого IP SNI отправляться не должен, а стоит %q", name)
	}
	// Резолвить нечего: адрес уже адрес.
	if _, present := server["domain_resolver"]; present {
		t.Error("для IP не нужен предварительный поиск имени")
	}
}

func TestRemoteDNSServerAcceptsIPv6Literal(t *testing.T) {
	server, err := remoteDNSServer("2606:4700:4700::1111", "proxy")
	if err != nil {
		t.Fatalf("IPv6 должен приниматься: %v", err)
	}
	if got := server["server"]; got != "2606:4700:4700::1111" {
		t.Errorf("адрес потерялся при разборе: %v", got)
	}
	tls, _ := server["tls"].(map[string]interface{})
	if _, present := tls["server_name"]; present {
		t.Error("для IPv6-литерала SNI тоже не отправляется")
	}
}

// Вторые адреса известных резолверов набирают по памяти не реже первых, и у
// них есть имя из карты — значит и SNI должен быть именем, а не отсутствовать.
func TestRemoteDNSServerKnownSecondaryAddresses(t *testing.T) {
	cases := map[string]string{
		"1.0.0.1":         "cloudflare-dns.com",
		"8.8.4.4":         "dns.google",
		"149.112.112.112": "dns.quad9.net",
		"94.140.14.14":    "dns.adguard-dns.com",
	}
	for address, wantSNI := range cases {
		server, err := remoteDNSServer(address, "proxy")
		if err != nil {
			t.Errorf("%s не принят: %v", address, err)
			continue
		}
		tls, _ := server["tls"].(map[string]interface{})
		if tls == nil || tls["server_name"] != wantSNI {
			t.Errorf("%s: SNI = %v, ожидался %s", address, tls["server_name"], wantSNI)
		}
	}
}
