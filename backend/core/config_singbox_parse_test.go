package core

import (
	"context"
	"encoding/json"
	"testing"

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
