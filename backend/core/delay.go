package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// DefaultDelayTestURL is a fast and lightweight captive portal check URL that returns HTTP 204 No Content.
// Using HTTP (port 80) avoids TLS handshake overhead to the test server, measuring true proxy round-trip time.
const DefaultDelayTestURL = "http://cp.cloudflare.com/generate_204"

// DefaultSpeedTestURL is the default payload URL (5 MB) used to test downstream throughput.
const DefaultSpeedTestURL = "https://speed.cloudflare.com/__down?bytes=5000000"

// maxConcurrentDelayTests defines the maximum number of ephemeral sing-box instances
// running simultaneously for delay and speed testing to prevent CPU and RAM spikes.
const maxConcurrentDelayTests = 8

var delayTestSem = make(chan struct{}, maxConcurrentDelayTests)

// createTestDialer boots an ephemeral sing-box instance for the given proxy link
// and extracts the outbound or endpoint network dialer.
// The returned cleanup function closes the instance and releases all context resources.
func createTestDialer(ctx context.Context, link string) (func(), N.Dialer, error) {
	outbound, err := ParseProxyLink(link)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse proxy link: %w", err)
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
		return nil, nil, fmt.Errorf("failed to marshal delay test config: %w", err)
	}

	boxCtx, cancelBox := context.WithCancel(ctx)
	boxCtx = include.Context(boxCtx)

	opt, err := sjson.UnmarshalExtendedContext[option.Options](boxCtx, raw)
	if err != nil {
		cancelBox()
		return nil, nil, fmt.Errorf("failed to parse delay test config: %w", err)
	}

	b, err := box.New(box.Options{
		Context: boxCtx,
		Options: opt,
	})
	if err != nil {
		cancelBox()
		return nil, nil, fmt.Errorf("failed to initialize delay test box: %w", err)
	}

	if err := b.Start(); err != nil {
		_ = b.Close()
		cancelBox()
		return nil, nil, fmt.Errorf("failed to start delay test box: %w", err)
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
		_ = b.Close()
		cancelBox()
		return nil, nil, fmt.Errorf("test dialer %q not found", testTag)
	}

	cleanup := func() {
		_ = b.Close()
		cancelBox()
	}

	return cleanup, dialer, nil
}

// MeasureProxyDelay tests the end-to-end HTTP 204 connectivity of any proxy link using an ephemeral sing-box instance.
// It acquires a slot from the worker pool semaphore to prevent concurrent resource exhaustion.
// It returns round-trip latency in milliseconds, or -1 with an error on failure.
func MeasureProxyDelay(ctx context.Context, link string, testURL string) (int, error) {
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}

	select {
	case delayTestSem <- struct{}{}:
		defer func() { <-delayTestSem }()
	case <-ctx.Done():
		return -1, ctx.Err()
	}

	cleanup, dialer, err := createTestDialer(ctx, link)
	if err != nil {
		return -1, err
	}
	defer cleanup()

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

// MeasureProxySpeed downloads a test chunk through the given proxy link and returns
// effective throughput in Megabits per second (Mbps).
func MeasureProxySpeed(ctx context.Context, link string, testURL string, byteCount int64) (float64, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	select {
	case delayTestSem <- struct{}{}:
		defer func() { <-delayTestSem }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	cleanup, dialer, err := createTestDialer(ctx, link)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	if testURL == "" {
		testURL = DefaultSpeedTestURL
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			destination := metadata.ParseSocksaddr(addr)
			return dialer.DialContext(ctx, network, destination)
		},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", getRandomUserAgent())

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("speedtest endpoint returned HTTP %d", resp.StatusCode)
	}

	var totalRead int64
	buf := make([]byte, 32*1024)
	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			totalRead += int64(n)
			if byteCount > 0 && totalRead >= byteCount {
				break
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return 0, rErr
		}
	}

	elapsed := time.Since(start)
	if totalRead == 0 || elapsed <= 0 {
		return 0, fmt.Errorf("no bytes transferred during speedtest")
	}

	mbps := (float64(totalRead) * 8.0) / (elapsed.Seconds() * 1_000_000.0)
	return mbps, nil
}
