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
		for name, outbound := range map[string]map[string]interface{}{
			"wireguard": wireguardOutbound(),
			"vless":     testOutbound(),
		} {
			cfg, err := GenerateConfig(outbound, Settings{TunMode: tunMode}, false, "cache.db", "secret")
			if err != nil {
				t.Fatalf("%s (tun=%v): GenerateConfig failed: %v", name, tunMode, err)
			}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("%s (tun=%v): marshal failed: %v", name, tunMode, err)
			}

			ctx := include.Context(context.Background())
			opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
			if err != nil {
				t.Fatalf("%s (tun=%v): sing-box rejected the config: %v\n%s", name, tunMode, err, raw)
			}
			if opts.DNS == nil || opts.DNS.Optimistic == nil {
				t.Errorf("%s (tun=%v): expected opts.DNS.Optimistic to be set", name, tunMode)
			}
			if opts.Experimental == nil || opts.Experimental.CacheFile == nil || !opts.Experimental.CacheFile.StoreDNS {
				t.Errorf("%s (tun=%v): expected opts.Experimental.CacheFile.StoreDNS to be true", name, tunMode)
			}
			t.Logf("%s (tun=%v): %d inbounds, %d outbounds, %d endpoints", name, tunMode, len(opts.Inbounds), len(opts.Outbounds), len(opts.Endpoints))
			if name == "wireguard" && len(opts.Endpoints) != 1 {
				t.Errorf("wireguard (tun=%v): endpoints = %d, want 1", tunMode, len(opts.Endpoints))
			}
		}
	}
}
