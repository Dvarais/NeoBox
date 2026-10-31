package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	N "github.com/sagernet/sing/common/network"
)

// DefaultDelayTestURL is a fast and lightweight captive portal check URL that returns HTTP 204 No Content.
// Using HTTP (port 80) avoids TLS handshake overhead to the test server, measuring true proxy round-trip time.
const DefaultDelayTestURL = "http://cp.cloudflare.com/generate_204"

// MeasureProxyDelay tests the end-to-end HTTP 204 connectivity of any proxy link using an ephemeral sing-box instance.
// It performs a genuine proxy handshake and returns round-trip latency in milliseconds, or -1 with an error on failure.
func MeasureProxyDelay(ctx context.Context, link string, testURL string) (int, error) {
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}

	outbound, err := ParseProxyLink(link)
	if err != nil {
		return -1, fmt.Errorf("failed to parse proxy link: %w", err)
	}

	// Work on a copy with a known test tag
	workOutbound := make(map[string]interface{}, len(outbound)+1)
	for k, v := range outbound {
		workOutbound[k] = v
	}
	const testTag = "test-proxy-node"
	workOutbound["tag"] = testTag

	outboundType, _ := workOutbound["type"].(string)
	isWireGuard := outboundType == "wireguard"

	config := map[string]interface{}{
		"log": map[string]interface{}{
			"level":  "panic",
			"output": os.DevNull,
		},
		"dns": map[string]interface{}{
			"servers": []map[string]interface{}{
				{
					"type": "local",
					"tag":  "dns-direct",
				},
			},
			"final": "dns-direct",
		},
	}

	if isWireGuard {
		config["endpoints"] = []interface{}{workOutbound}
		config["outbounds"] = []interface{}{
			map[string]interface{}{"type": "direct", "tag": "direct"},
		}
	} else {
		config["outbounds"] = []interface{}{
			workOutbound,
			map[string]interface{}{"type": "direct", "tag": "direct"},
		}
	}

	raw, err := json.Marshal(config)
	if err != nil {
		return -1, fmt.Errorf("failed to marshal delay test config: %w", err)
	}

	boxCtx, cancelBox := context.WithCancel(ctx)
	defer cancelBox()
	boxCtx = include.Context(boxCtx)

	opt, err := sjson.UnmarshalExtendedContext[option.Options](boxCtx, raw)
	if err != nil {
		return -1, fmt.Errorf("failed to parse delay test config: %w", err)
	}

	b, err := box.New(box.Options{
		Context: boxCtx,
		Options: opt,
	})
	if err != nil {
		return -1, fmt.Errorf("failed to initialize delay test box: %w", err)
	}
	defer b.Close()

	if err := b.Start(); err != nil {
		return -1, fmt.Errorf("failed to start delay test box: %w", err)
	}

	var dialer N.Dialer
	if isWireGuard {
		if ep, ok := b.Endpoint().Get(testTag); ok {
			dialer = ep
		}
	} else {
		if ob, ok := b.Outbound().Outbound(testTag); ok {
			dialer = ob
		}
	}

	if dialer == nil {
		return -1, fmt.Errorf("test dialer %q not found", testTag)
	}

	if testURL == "" {
		testURL = DefaultDelayTestURL
	}

	// URLTest runs the HTTP HEAD request through the proxy dialer
	delay, err := urltest.URLTest(ctx, testURL, dialer)
	if err != nil {
		return -1, err
	}
	if delay == 0 {
		return 1, nil
	}
	return int(delay), nil
}
