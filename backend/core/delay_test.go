package core

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMeasureProxyDelay_InvalidLink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	delay, err := MeasureProxyDelay(ctx, "invalid-scheme://abc", "")
	if err == nil {
		t.Fatal("expected error for invalid link")
	}
	if delay != -1 {
		t.Errorf("delay = %d, want -1", delay)
	}
}

func TestMeasureProxyDelay_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Using a TEST-NET-3 IP that will not respond immediately
	link := "vless://96c4d7b2-d3cf-4279-b1d5-2244249a5629@203.0.113.10:443?type=tcp#timeout-test"
	delay, err := MeasureProxyDelay(ctx, link, "")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if delay != -1 {
		t.Errorf("delay = %d, want -1", delay)
	}
}

func TestMeasureProxyDelay_RealLocalProxy(t *testing.T) {
	// 1. Create a dummy target 204 server
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer targetServer.Close()

	// 2. Create a minimal forward HTTP proxy server
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for proxy: %v", err)
	}
	defer proxyListener.Close()

	proxyServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				// Tunnel connection
				destConn, err := net.DialTimeout("tcp", r.Host, 2*time.Second)
				if err != nil {
					http.Error(w, err.Error(), http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "hijacking not supported", http.StatusInternalServerError)
					return
				}
				clientConn, _, err := hijacker.Hijack()
				if err != nil {
					destConn.Close()
					return
				}
				go func() {
					defer clientConn.Close()
					defer destConn.Close()
					buf := make([]byte, 32*1024)
					for {
						n, err := clientConn.Read(buf)
						if n > 0 {
							if _, wErr := destConn.Write(buf[:n]); wErr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}()
				buf := make([]byte, 32*1024)
				for {
					n, err := destConn.Read(buf)
					if n > 0 {
						if _, wErr := clientConn.Write(buf[:n]); wErr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			} else {
				w.WriteHeader(http.StatusOK)
			}
		}),
	}
	go proxyServer.Serve(proxyListener)
	defer proxyServer.Close()

	// 3. Test MeasureProxyDelay with this local proxy link
	proxyAddr := proxyListener.Addr().String()
	link := fmt.Sprintf("http://%s#local-test-proxy", proxyAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	delay, err := MeasureProxyDelay(ctx, link, targetServer.URL)
	if err != nil {
		t.Fatalf("MeasureProxyDelay failed: %v", err)
	}
	if delay <= 0 {
		t.Errorf("expected positive delay, got %d", delay)
	}
	t.Logf("Measured local proxy HTTP 204 delay: %d ms", delay)
}

func TestMeasureProxySpeed_RealLocalProxy(t *testing.T) {
	// 1. Create a payload server returning 100 KB
	const testPayloadSize = 100 * 1024
	payloadData := make([]byte, testPayloadSize)
	for i := range payloadData {
		payloadData[i] = byte(i % 256)
	}

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", testPayloadSize))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payloadData)
	}))
	defer targetServer.Close()

	// 2. Create minimal forward HTTP proxy
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for proxy: %v", err)
	}
	defer proxyListener.Close()

	proxyServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				destConn, err := net.DialTimeout("tcp", r.Host, 2*time.Second)
				if err != nil {
					http.Error(w, err.Error(), http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusOK)
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "hijacking not supported", http.StatusInternalServerError)
					return
				}
				clientConn, _, err := hijacker.Hijack()
				if err != nil {
					destConn.Close()
					return
				}
				go func() {
					defer clientConn.Close()
					defer destConn.Close()
					buf := make([]byte, 32*1024)
					for {
						n, err := clientConn.Read(buf)
						if n > 0 {
							if _, wErr := destConn.Write(buf[:n]); wErr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}()
				buf := make([]byte, 32*1024)
				for {
					n, err := destConn.Read(buf)
					if n > 0 {
						if _, wErr := clientConn.Write(buf[:n]); wErr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			} else {
				w.WriteHeader(http.StatusOK)
			}
		}),
	}
	go proxyServer.Serve(proxyListener)
	defer proxyServer.Close()

	proxyAddr := proxyListener.Addr().String()
	link := fmt.Sprintf("http://%s#local-speed-proxy", proxyAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mbps, err := MeasureProxySpeed(ctx, link, targetServer.URL, testPayloadSize)
	if err != nil {
		t.Fatalf("MeasureProxySpeed failed: %v", err)
	}
	if mbps <= 0 {
		t.Errorf("expected positive throughput mbps, got %f", mbps)
	}
	t.Logf("Measured local proxy throughput: %.2f Mbps", mbps)
}

func TestMeasureProxyDelay_ConcurrencySemaphore(t *testing.T) {
	// Verify that multiple goroutines can safely acquire and release the semaphore without deadlock
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	link := "invalid-scheme://test"
	done := make(chan bool, 12)
	for i := 0; i < 12; i++ {
		go func() {
			_, _ = MeasureProxyDelay(ctx, link, "")
			done <- true
		}()
	}

	for i := 0; i < 12; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("semaphore deadlock or timeout occurred")
		}
	}
}
