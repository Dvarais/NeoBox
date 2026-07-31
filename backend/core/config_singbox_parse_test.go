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
	for name, outbound := range map[string]map[string]interface{}{
		"wireguard": wireguardOutbound(),
		"vless":     testOutbound(),
	} {
		cfg, err := GenerateConfig(outbound, Settings{}, false, "cache.db", "secret")
		if err != nil {
			t.Fatalf("%s: GenerateConfig failed: %v", name, err)
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("%s: marshal failed: %v", name, err)
		}

		ctx := include.Context(context.Background())
		opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, raw)
		if err != nil {
			t.Fatalf("%s: sing-box rejected the config: %v\n%s", name, err, raw)
		}
		t.Logf("%s: %d outbounds, %d endpoints", name, len(opts.Outbounds), len(opts.Endpoints))
		if name == "wireguard" && len(opts.Endpoints) != 1 {
			t.Errorf("wireguard: endpoints = %d, want 1", len(opts.Endpoints))
		}
	}
}
