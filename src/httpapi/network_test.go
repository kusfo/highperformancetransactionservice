package httpapi

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func networkRuntime(t *testing.T, configure func(*Config), handler http.Handler) (string, *http.Client) {
	t.Helper()
	cfg, client := tlsConfigForTest(t)
	configure(&cfg)
	runtime, err := New(cfg, handler, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("runtime cleanup timed out")
		}
	})
	return listener.Addr().String(), client
}
func TestNetworkBodyBoundaries(t *testing.T) {
	transfer := make(chan bool, 8)
	addr, client := networkRuntime(t, func(c *Config) { c.MaxBodyBytes = 64 }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transfer <- len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked"
		var value struct {
			Name string `json:"name"`
		}
		if DecodeJSON(w, r, &value) {
			WriteJSON(w, 200, map[string]bool{"ok": true})
		}
	}))
	exact := `{"name":"` + strings.Repeat("x", 64-len(`{"name":""}`)) + `"}`
	if len(exact) != 64 {
		t.Fatal("incorrect fixture boundary")
	}
	cases := []struct {
		name, body, encoding string
		chunked              bool
		status               int
	}{
		{"exact limit", exact, "", false, 200}, {"one byte over", exact + " ", "", false, 413},
		{"chunked exact", exact, "", true, 200}, {"chunked over", exact + " ", "", true, 413},
		{"compressed", `{"name":"x"}`, "gzip", false, 415},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest("POST", "https://"+addr, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			if tc.encoding != "" {
				request.Header.Set("Content-Encoding", tc.encoding)
			}
			if tc.chunked {
				request.ContentLength = -1
				request.TransferEncoding = []string{"chunked"}
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", response.StatusCode, tc.status)
			}
			// Known-length overflow is rejected before dispatch; all other cases reach it.
			if tc.name != "one byte over" {
				select {
				case chunked := <-transfer:
					if chunked != tc.chunked {
						t.Fatal("unexpected wire transfer encoding")
					}
				case <-time.After(time.Second):
					t.Fatal("handler did not run")
				}
			}
		})
	}
}
func TestNetworkHeadersAndPlaintext(t *testing.T) {
	var calls atomic.Int32
	addr, client := networkRuntime(t, func(c *Config) { c.MaxHeaderBytes = 1024 }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); WriteJSON(w, 200, nil) }))
	plain := &http.Client{Timeout: time.Second}
	response, err := plain.Get("http://" + addr)
	if err != nil {
		t.Fatalf("plaintext rejection: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("plaintext status: %d", response.StatusCode)
	}
	if calls.Load() != 0 {
		t.Fatal("plaintext reached handler")
	}
	request, _ := http.NewRequest("GET", "https://"+addr, nil)
	request.Header.Set("X-Oversized", strings.Repeat("x", 16<<10))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 431 || calls.Load() != 0 {
		t.Fatalf("header limit status: %d, calls %d", response.StatusCode, calls.Load())
	}
}
func TestNetworkReadDeadlines(t *testing.T) {
	for _, mode := range []string{"headers", "body"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			addr, client := networkRuntime(t, func(c *Config) {
				c.ReadHeaderTimeout = 100 * time.Millisecond
				c.ReadTimeout = 200 * time.Millisecond
				if mode == "headers" {
					// A body deadline must not mask a missing header deadline.
					c.ReadTimeout = 5 * time.Second
				}
			}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				var body struct {
					Name string `json:"name"`
				}
				if DecodeJSON(w, r, &body) {
					WriteJSON(w, 200, nil)
				}
			}))
			transport := client.Transport.(*http.Transport)
			connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, transport.TLSClientConfig.Clone())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			wire := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\n"
			if mode == "body" {
				wire += "Content-Length: 100\r\n\r\n{"
			}
			if _, err := io.WriteString(connection, wire); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), nil)
			if err != nil {
				if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					t.Fatalf("server failed to enforce %s read deadline", mode)
				}
			} else {
				_, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() {
					t.Fatal("response hung until client deadline")
				}
				if response.StatusCode < 400 {
					t.Fatalf("incomplete request accepted: %d", response.StatusCode)
				}
			}
			if mode == "headers" {
				select {
				case <-entered:
					t.Fatal("incomplete headers dispatched")
				default:
				}
			} else {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("body test did not reach decoder")
				}
			}
		})
	}
}
