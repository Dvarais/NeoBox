package core

import (
	"context"
	"encoding/json"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
)

// The generated config must be accepted by the very same parser CoreManager
// uses, otherwise a field name that looks right here fails at connect time.
func TestGeneratedConfigParsesInSingbox(t *testing.T) {
	for _, tunMode := range []bool{false, true} {
		for _, bypassRu := range []bool{false, true} {
			for name, outbound := range map[string]map[string]interface{}{
				"wireguard": wireguardOutbound(),
				"vless":     testOutbound(),
			} {
				cfg, err := GenerateConfig(outbound, Settings{TunMode: tunMode, BypassRu: bypassRu}, false, "cache.db", "secret")
				if err != nil {
					t.Fatalf("%s (tun=%v, bypassRu=%v): GenerateConfig failed: %v", name, tunMode, bypassRu, err)
				}
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatalf("%s (tun=%v, bypassRu=%v): marshal failed: %v", name, tunMode, bypassRu, err)
				}

				ctx := include.Context(context.Background())
				opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
				if err != nil {
					t.Fatalf("%s (tun=%v, bypassRu=%v): sing-box rejected the config: %v\n%s", name, tunMode, bypassRu, err, raw)
				}
				instance, err := box.New(box.Options{
					Context: ctx,
					Options: opts,
				})
				if err != nil {
					t.Fatalf("%s (tun=%v, bypassRu=%v): box.New failed: %v", name, tunMode, bypassRu, err)
				}
				instance.Close()
				if opts.DNS == nil || opts.DNS.Optimistic == nil {
					t.Errorf("%s (tun=%v, bypassRu=%v): expected opts.DNS.Optimistic to be set", name, tunMode, bypassRu)
				}
				if opts.Experimental == nil || opts.Experimental.CacheFile == nil || !opts.Experimental.CacheFile.StoreDNS {
					t.Errorf("%s (tun=%v, bypassRu=%v): expected opts.Experimental.CacheFile.StoreDNS to be true", name, tunMode, bypassRu)
				}
				if bypassRu {
					if len(opts.HTTPClients) == 0 {
						t.Errorf("%s (tun=%v, bypassRu=%v): expected HTTPClients to be populated", name, tunMode, bypassRu)
					}
					if opts.Route == nil || opts.Route.DefaultHTTPClient != "ruleset-client" {
						t.Errorf("%s (tun=%v, bypassRu=%v): expected DefaultHTTPClient ruleset-client", name, tunMode, bypassRu)
					}
					if len(opts.Route.RuleSet) != 2 {
						t.Errorf("%s (tun=%v, bypassRu=%v): expected 2 rule_sets, got %d", name, tunMode, bypassRu, len(opts.Route.RuleSet))
					}
				}
				t.Logf("%s (tun=%v, bypassRu=%v): %d inbounds, %d outbounds, %d endpoints", name, tunMode, bypassRu, len(opts.Inbounds), len(opts.Outbounds), len(opts.Endpoints))
				if name == "wireguard" && len(opts.Endpoints) != 1 {
					t.Errorf("wireguard (tun=%v, bypassRu=%v): endpoints = %d, want 1", tunMode, bypassRu, len(opts.Endpoints))
				}
			}
		}
	}
}
func TestGeneratedConfigParsesInSingbox_TlsFragment(t *testing.T) {
	trueVal := true
	outbound := map[string]interface{}{
		"type":        "trojan",
		"tag":         "proxy",
		"server":      testServerIP,
		"server_port": 443,
		"password":    "secret",
		"tls": map[string]interface{}{
			"enabled":     true,
			"server_name": "example.com",
		},
	}
	cfg, err := GenerateConfig(outbound, Settings{TlsFragment: &trueVal}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	ctx := include.Context(context.Background())
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		t.Fatalf("sing-box rejected config with TlsFragment: %v", err)
	}
	if len(opts.Outbounds) == 0 {
		t.Fatal("no outbounds in parsed config")
	}
	trojanOpt, ok := opts.Outbounds[0].Options.(*option.TrojanOutboundOptions)
	if !ok {
		t.Fatalf("outbound options type = %T, want *option.TrojanOutboundOptions", opts.Outbounds[0].Options)
	}
	if trojanOpt.TLS == nil || !trojanOpt.TLS.RecordFragment {
		t.Errorf("trojanOpt.TLS.RecordFragment = %v, want true", trojanOpt.TLS != nil && trojanOpt.TLS.RecordFragment)
	}
}


func TestGenerateConfig_SelectorWithCandidates(t *testing.T) {
	node1 := map[string]interface{}{
		"type":        "vless",
		"tag":         "de-node-1",
		"server":      "198.51.100.1",
		"server_port": 443,
	}
	node2 := map[string]interface{}{
		"type":        "trojan",
		"tag":         "nl-node-2",
		"server":      "198.51.100.2",
		"server_port": 8443,
	}
	candidates := []map[string]interface{}{node1, node2}

	cfg, err := GenerateConfigWithCandidates(candidates, node1, Settings{TunMode: false, BypassRu: true}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed: %v", err)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	ctx := include.Context(context.Background())
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		t.Fatalf("sing-box rejected config with selector: %v\n%s", err, string(raw))
	}
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: opts,
	})
	if err != nil {
		t.Fatalf("sing-box box.New failed: %v", err)
	}
	instance.Close()

	// Verify selector is present and configured
	var foundSelector bool
	for _, ob := range opts.Outbounds {
		if ob.Type == "selector" && ob.Tag == OutboundSelectorTag {
			foundSelector = true
			selOpt, ok := ob.Options.(*option.SelectorOutboundOptions)
			if !ok {
				t.Fatalf("selector options type = %T, want *option.SelectorOutboundOptions", ob.Options)
			}
			if selOpt.Default != "de-node-1" {
				t.Errorf("selector default = %q, want de-node-1", selOpt.Default)
			}
			if len(selOpt.Outbounds) != 3 {
				t.Errorf("selector outbounds count = %d, want 3", len(selOpt.Outbounds))
			}
		}
	}
	if !foundSelector {
		t.Fatalf("selector outbound %q not found in parsed outbounds", OutboundSelectorTag)
	}

	// Verify route.final points to selector
	if opts.Route == nil || opts.Route.Final != OutboundSelectorTag {
		t.Errorf("route.final = %v, want %q", opts.Route.Final, OutboundSelectorTag)
	}
}

func TestGenerateConfig_URLTestAutoGroup(t *testing.T) {
	node1 := map[string]interface{}{
		"type":        "vless",
		"tag":         "de-node-1",
		"server":      "198.51.100.1",
		"server_port": 443,
	}
	node2 := map[string]interface{}{
		"type":        "trojan",
		"tag":         "nl-node-2",
		"server":      "198.51.100.2",
		"server_port": 8443,
	}
	candidates := []map[string]interface{}{node1, node2}

	cfg, err := GenerateConfigWithCandidates(candidates, node1, Settings{TunMode: false}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed: %v", err)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	ctx := include.Context(context.Background())
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		t.Fatalf("sing-box rejected config with urltest: %v\n%s", err, string(raw))
	}
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: opts,
	})
	if err != nil {
		t.Fatalf("sing-box box.New failed: %v", err)
	}
	instance.Close()

	var foundAuto, foundSelector bool
	for _, ob := range opts.Outbounds {
		if ob.Type == "urltest" && ob.Tag == OutboundAutoTag {
			foundAuto = true
			urlOpt, ok := ob.Options.(*option.URLTestOutboundOptions)
			if !ok {
				t.Fatalf("urltest options type = %T, want *option.URLTestOutboundOptions", ob.Options)
			}
			if len(urlOpt.Outbounds) != 2 {
				t.Errorf("urltest outbounds count = %d, want 2", len(urlOpt.Outbounds))
			}
		}
		if ob.Type == "selector" && ob.Tag == OutboundSelectorTag {
			foundSelector = true
			selOpt, ok := ob.Options.(*option.SelectorOutboundOptions)
			if !ok {
				t.Fatalf("selector options type = %T, want *option.SelectorOutboundOptions", ob.Options)
			}
			// selector should include auto-out in its options
			hasAuto := false
			for _, tag := range selOpt.Outbounds {
				if tag == OutboundAutoTag {
					hasAuto = true
					break
				}
			}
			if !hasAuto {
				t.Errorf("selector outbounds %v does not contain %q", selOpt.Outbounds, OutboundAutoTag)
			}
		}
	}

	if !foundAuto {
		t.Fatalf("urltest outbound %q not found in parsed outbounds", OutboundAutoTag)
	}
	if !foundSelector {
		t.Fatalf("selector outbound %q not found in parsed outbounds", OutboundSelectorTag)
	}
}

func TestGenerateConfig_SelectorDefaultAuto(t *testing.T) {
	node1 := map[string]interface{}{
		"type":              "vless",
		"tag":               "de-node-1",
		"server":            "1.1.1.1",
		"server_port":       443,
		"uuid":              "a1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d",
		"_selector_default": OutboundAutoTag,
	}
	node2 := map[string]interface{}{
		"type":        "vless",
		"tag":         "nl-node-2",
		"server":      "1.0.0.1",
		"server_port": 443,
		"uuid":        "b2c3d4e5-f6a7-8b9c-0d1e-2f3a4b5c6d7e",
	}

	candidates := []map[string]interface{}{node1, node2}

	cfg, err := GenerateConfigWithCandidates(candidates, node1, Settings{TunMode: false}, false, "cache.db", "secret")
	if err != nil {
		t.Fatalf("GenerateConfigWithCandidates failed: %v", err)
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal config failed: %v", err)
	}

	ctx := include.Context(context.Background())
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, cfgJSON)
	if err != nil {
		t.Fatalf("sjson.UnmarshalExtendedContext failed: %v", err)
	}

	for _, ob := range opts.Outbounds {
		if ob.Type == "selector" && ob.Tag == OutboundSelectorTag {
			selOpt, ok := ob.Options.(*option.SelectorOutboundOptions)
			if !ok {
				t.Fatalf("selector options type = %T, want *option.SelectorOutboundOptions", ob.Options)
			}
			if selOpt.Default != OutboundAutoTag {
				t.Errorf("selector default = %q, want %q", selOpt.Default, OutboundAutoTag)
			}
			return
		}
	}
	t.Fatalf("selector outbound %q not found in parsed outbounds", OutboundSelectorTag)
}
