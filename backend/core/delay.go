package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
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

var (
	staticDelayDNSJSON    = []byte(`{"servers":[{"type":"local","tag":"dns-direct"}],"final":"dns-direct"}`)
	staticDelayDirectJSON = []byte(`{"type":"direct","tag":"direct"}`)
)

var delayTestSem = make(chan struct{}, maxConcurrentDelayTests)

// createTestDialer boots an ephemeral sing-box instance for the given proxy link
// and extracts the outbound or endpoint network dialer.
// The returned cleanup function closes the instance and releases all context resources.
func createTestDialer(ctx context.Context, link string) (func(), N.Dialer, error) {
	workOutbound, err := ParseProxyLink(link)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse proxy link: %w", err)
	}

	const testTag = "test-proxy-node"
	workOutbound["tag"] = testTag

	outboundType, _ := workOutbound["type"].(string)
	isWireGuard := outboundType == "wireguard"

	rawOutbound, err := json.Marshal(workOutbound)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal delay test outbound: %w", err)
	}

	boxCtx, cancelBox := context.WithCancel(ctx)
	boxCtx = include.Context(boxCtx)

	var dnsOpts option.DNSOptions
	if err := dnsOpts.UnmarshalJSONContext(boxCtx, staticDelayDNSJSON); err != nil {
		cancelBox()
		return nil, nil, fmt.Errorf("failed to parse delay test dns: %w", err)
	}

	var directOb option.Outbound
	if err := directOb.UnmarshalJSONContext(boxCtx, staticDelayDirectJSON); err != nil {
		cancelBox()
		return nil, nil, fmt.Errorf("failed to parse delay test direct outbound: %w", err)
	}

	opts := option.Options{
		Log: &option.LogOptions{
			Level:  "panic",
			Output: os.DevNull,
		},
		DNS: &dnsOpts,
	}

	if isWireGuard {
		var ep option.Endpoint
		if err := ep.UnmarshalJSONContext(boxCtx, rawOutbound); err != nil {
			cancelBox()
			return nil, nil, fmt.Errorf("failed to parse delay test endpoint: %w", err)
		}
		opts.Endpoints = []option.Endpoint{ep}
		opts.Outbounds = []option.Outbound{directOb}
	} else {
		var ob option.Outbound
		if err := ob.UnmarshalJSONContext(boxCtx, rawOutbound); err != nil {
			cancelBox()
			return nil, nil, fmt.Errorf("failed to parse delay test outbound: %w", err)
		}
		opts.Outbounds = []option.Outbound{ob, directOb}
	}

	b, err := box.New(box.Options{
		Context: boxCtx,
		Options: opts,
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

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = b.Close()
			cancelBox()
		})
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
	defer transport.CloseIdleConnections()

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

	var reader io.Reader = resp.Body
	if byteCount > 0 {
		reader = io.LimitReader(resp.Body, byteCount)
	}

	totalRead, err := io.Copy(io.Discard, reader)
	if err != nil {
		return 0, err
	}

	elapsed := time.Since(start)
	if totalRead == 0 || elapsed <= 0 {
		return 0, fmt.Errorf("no bytes transferred during speedtest")
	}
	if elapsed < 1*time.Millisecond {
		elapsed = 1 * time.Millisecond
	}

	mbps := (float64(totalRead) * 8.0) / (elapsed.Seconds() * 1_000_000.0)
	return mbps, nil
}
