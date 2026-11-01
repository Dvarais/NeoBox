package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolOf_OversizedLink(t *testing.T) {
	oversized := "vless://" + strings.Repeat("a", 5000)
	if got := ProtocolOf(oversized); got != "" {
		t.Errorf("expected empty protocol for oversized link, got %q", got)
	}
}

func TestServerEndpoint_IPv6BracketTrimming(t *testing.T) {
	host, port := ServerEndpoint(map[string]interface{}{
		"server":      "[2001:db8::1]",
		"server_port": int32(8443),
	})
	if host != "2001:db8::1" {
		t.Errorf("expected 2001:db8::1, got %q", host)
	}
	if port != 8443 {
		t.Errorf("expected port 8443, got %d", port)
	}
}

func TestFieldDecoders_ExtendedTypes(t *testing.T) {
	m := map[string]interface{}{
		"port_int64":   int64(443),
		"port_int32":   int32(8080),
		"port_uint":    uint(1080),
		"port_jsonNum": json.Number("9000"),
		"port_float32": float32(80),
		"str_int64":    int64(123456),
		"str_jsonNum":  json.Number("654321"),
	}

	if fieldInt(m, "port_int64") != 443 {
		t.Errorf("expected 443, got %d", fieldInt(m, "port_int64"))
	}
	if fieldInt(m, "port_int32") != 8080 {
		t.Errorf("expected 8080, got %d", fieldInt(m, "port_int32"))
	}
	if fieldInt(m, "port_uint") != 1080 {
		t.Errorf("expected 1080, got %d", fieldInt(m, "port_uint"))
	}
	if fieldInt(m, "port_jsonNum") != 9000 {
		t.Errorf("expected 9000, got %d", fieldInt(m, "port_jsonNum"))
	}
	if fieldInt(m, "port_float32") != 80 {
		t.Errorf("expected 80, got %d", fieldInt(m, "port_float32"))
	}

	if fieldString(m, "str_int64") != "123456" {
		t.Errorf("expected 123456, got %q", fieldString(m, "str_int64"))
	}
	if fieldString(m, "str_jsonNum") != "654321" {
		t.Errorf("expected 654321, got %q", fieldString(m, "str_jsonNum"))
	}
}

func TestNormaliseProcessName_ControlChars(t *testing.T) {
	if _, ok := NormaliseProcessName("bad\x00proc.exe"); ok {
		t.Error("expected refusal for null byte in process name")
	}
	if _, ok := NormaliseProcessName("bad\x1fproc.exe"); ok {
		t.Error("expected refusal for control char in process name")
	}
	if name, ok := NormaliseProcessName("good_proc.exe"); !ok || name != "good_proc.exe" {
		t.Errorf("expected valid name, got %q, %v", name, ok)
	}
}

func TestSuggestRuleTarget_ConsecutiveDots(t *testing.T) {
	if _, _, ok := SuggestRuleTarget("example..com", ""); ok {
		t.Error("expected refusal for consecutive dots in host")
	}
}

func TestIsDoHHostname(t *testing.T) {
	if isDoHHostname("example..com") {
		t.Error("expected false for consecutive dots")
	}
	if isDoHHostname(".example.com") {
		t.Error("expected false for leading dot")
	}
	if isDoHHostname("example.com.") {
		t.Error("expected false for trailing dot")
	}
	if !isDoHHostname("dns.example.com") {
		t.Error("expected true for valid domain")
	}
}

func TestDecodeBase64Any(t *testing.T) {
	original := "Hello, World! 12345"
	stdPadded := "SGVsbG8sIFdvcmxkISAxMjM0NQ=="
	stdUnpadded := "SGVsbG8sIFdvcmxkISAxMjM0NQ"

	if b, err := decodeBase64Any(stdPadded); err != nil || string(b) != original {
		t.Errorf("failed std padded decode: %v, got %q", err, string(b))
	}
	if b, err := decodeBase64Any(stdUnpadded); err != nil || string(b) != original {
		t.Errorf("failed std unpadded decode: %v, got %q", err, string(b))
	}
}

func TestParseProxyLink_MalformedNoUser(t *testing.T) {
	malformedLinks := []string{
		"vless://example.com:443",
		"trojan://example.com:443",
		"tuic://example.com:443",
		"hy2://example.com:443",
		"hysteria2://example.com:443",
		"wireguard://example.com:51820?address=10.0.0.2/32&publickey=xyz",
	}

	for _, link := range malformedLinks {
		outbound, err := ParseProxyLink(link)
		if err == nil {
			t.Errorf("expected error for malformed link %q, got outbound: %+v", link, outbound)
		}
	}
}
