package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configForTest() Config {
	cfg := DefaultConfig()
	cfg.TLSCertFile = "test-cert"
	cfg.TLSKeyFile = "test-key"
	return cfg
}
func handlerForTest(t *testing.T, cfg Config, h http.Handler, logs io.Writer) http.Handler {
	t.Helper()
	runtime, err := New(cfg, h, logs)
	if err != nil {
		t.Fatal(err)
	}
	return runtime.server.Handler
}
func TestLoadConfig(t *testing.T) {
	env := map[string]string{"HTTP_TLS_CERT_FILE": "cert.pem", "HTTP_TLS_KEY_FILE": "key.pem", "HTTP_ADDR": "127.0.0.1:0", "HTTP_MAX_BODY_BYTES": "1024"}
	cfg, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil || cfg.MaxBodyBytes != 1024 {
		t.Fatalf("load config: %v", err)
	}
	for _, key := range []string{"HTTP_MAX_BODY_BYTES", "HTTP_MAX_HEADER_BYTES", "HTTP_READ_TIMEOUT", "HTTP_ADDR"} {
		t.Run(key, func(t *testing.T) {
			_, err := LoadConfig(func(k string) string {
				if k == key {
					return "secret-invalid-value"
				}
				return env[k]
			})
			if err == nil || strings.Contains(err.Error(), "secret-invalid-value") {
				t.Fatalf("unsafe config error: %v", err)
			}
		})
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.TLSKeyFile = "" }, func(c *Config) { c.MaxBodyBytes = 0 }, func(c *Config) { c.ShutdownTimeout = 0 }, func(c *Config) { c.WriteTimeout = c.RequestTimeout }} {
		c := configForTest()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatal("expected invalid configuration")
		}
	}
}
func TestDecodeAndLimit(t *testing.T) {
	cfg := configForTest()
	cfg.MaxBodyBytes = 64
	handler := handlerForTest(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if DecodeJSON(w, r, &body) {
			WriteJSON(w, 200, map[string]bool{"ok": true})
		}
	}), io.Discard)
	cases := []struct {
		name, body, contentType string
		length                  int64
		status                  int
	}{
		{"valid", `{"name":"test"}`, "application/json", -1, 200},
		{"malformed", `{"name":`, "application/json", -1, 400},
		{"unknown", `{"password":"secret-password"}`, "application/json", -1, 400},
		{"trailing", `{"name":"x"} {}`, "application/json", -1, 400},
		{"null", `null`, "application/json", -1, 400},
		{"array", `[]`, "application/json", -1, 400},
		{"empty", ``, "application/json", -1, 400},
		{"wrong type", `{}`, "text/plain", -1, 415},
		{"streamed overflow", `{"name":"` + strings.Repeat("x", 65) + `"}`, "application/json", -1, 413},
		{"known overflow", `{}`, "application/json", 65, 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "https://example.com/", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			request.ContentLength = tc.length
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing JSON safety headers")
			}
			if tc.status != 200 {
				var e Error
				if json.Unmarshal(response.Body.Bytes(), &e) != nil || e.Code == "" || strings.Contains(response.Body.String(), "secret-password") {
					t.Fatal("unsafe error response")
				}
			}
		})
	}
}
func TestMetadataOnlyLoggingAndPanic(t *testing.T) {
	var logs bytes.Buffer
	cfg := configForTest()
	handler := handlerForTest(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("panic-secret-hash") }), &logs)
	request := httptest.NewRequest("POST", "https://example.com/path-secret-token?password=query-secret-password", strings.NewReader(`{"password":"body-secret-password"}`))
	request.Header.Set("Authorization", "Bearer header-secret-token")
	request.Header.Set("Cookie", "session=cookie-secret-token")
	request.Header.Set("X-Request-ID", "client-secret-id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 500 {
		t.Fatal(response.Code)
	}
	if response.Header().Get("X-Request-ID") == "client-secret-id" {
		t.Fatal("trusted client request id")
	}
	for _, secret := range []string{"panic-secret-hash", "path-secret-token", "query-secret-password", "body-secret-password", "header-secret-token", "cookie-secret-token", "client-secret-id"} {
		if strings.Contains(logs.String(), secret) || strings.Contains(response.Body.String(), secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["msg"] != "http_request" || event["status"] != float64(500) {
		t.Fatalf("unexpected event: %v", event)
	}
	if len(event) != 7 {
		t.Fatalf("unexpected logged fields: %v", event)
	}
}
func TestTimeoutAndClientCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{true: "client cancelled", false: "deadline"}[cancelled], func(t *testing.T) {
			cfg := configForTest()
			cfg.RequestTimeout = 20 * time.Millisecond
			observed := make(chan error, 1)
			release := make(chan struct{})
			defer close(release)
			handler := handlerForTest(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
				observed <- r.Context().Err()
				<-release
			}), io.Discard)
			request := httptest.NewRequest("GET", "https://example.com/", nil)
			ctx, cancel := context.WithCancel(request.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			request = request.WithContext(ctx)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			code := "REQUEST_TIMEOUT"
			if cancelled {
				code = "REQUEST_CANCELLED"
			}
			if response.Code != 503 || !strings.Contains(response.Body.String(), code) {
				t.Fatalf("unexpected cancellation response: %d %s", response.Code, response.Body.String())
			}
			select {
			case err := <-observed:
				if err == nil {
					t.Fatal("missing cancellation")
				}
			case <-time.After(time.Second):
				t.Fatal("handler did not receive cancellation")
			}
		})
	}
}
func TestWriteJSONEncodingFailure(t *testing.T) {
	response := httptest.NewRecorder()
	WriteJSON(response, 200, make(chan int))
	if response.Code != 500 || !strings.Contains(response.Body.String(), "INTERNAL_ERROR") {
		t.Fatal("encoding error was not safe")
	}
}

func tlsConfigForTest(t *testing.T) (Config, *http.Client) {
	t.Helper()
	source := httptest.NewTLSServer(http.NotFoundHandler())
	defer source.Close()
	cert := source.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := configForTest()
	cfg.Addr = "127.0.0.1:0"
	cfg.TLSCertFile = filepath.Join(dir, "cert.pem")
	cfg.TLSKeyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(cfg.TLSCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.TLSKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(parsed)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(func() { client.CloseIdleConnections() })
	return cfg, client
}
func TestTLSGracefulShutdown(t *testing.T) {
	cfg, client := tlsConfigForTest(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	runtime, err := New(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		WriteJSON(w, 200, map[string]bool{"ok": true})
	}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, listener) }()
	response := make(chan error, 1)
	go func() {
		r, err := client.Get("https://" + listener.Addr().String())
		if err == nil {
			_, err = io.ReadAll(r.Body)
			_ = r.Body.Close()
			if r.StatusCode != 200 {
				err = io.ErrUnexpectedEOF
			}
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("listener still open")
	}
}
func TestShutdownDeadlineCancelsInFlight(t *testing.T) {
	cfg, client := tlsConfigForTest(t)
	cfg.ShutdownTimeout = 20 * time.Millisecond
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	runtime, err := New(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) }), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, listener) }()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		r, _ := client.Get("https://" + listener.Addr().String())
		if r != nil {
			_ = r.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected shutdown deadline error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("request context not cancelled")
	}
	<-clientDone
}
func TestInvalidTLSClosesListener(t *testing.T) {
	runtime, err := New(configForTest(), http.NotFoundHandler(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Serve(context.Background(), listener) == nil {
		t.Fatal("invalid TLS accepted")
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("listener leaked")
	}
}

func TestPanicDiscardsBufferedResponse(t *testing.T) {
	for _, mode := range []string{"headers", "status", "body"} {
		t.Run(mode, func(t *testing.T) {
			var logs bytes.Buffer
			handler := handlerForTest(t, configForTest(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Set-Cookie", "session=secret-header-token")
				w.Header().Set("Content-Type", "text/plain")
				if mode == "status" {
					w.WriteHeader(201)
				}
				if mode == "body" {
					WriteJSON(w, 201, map[string]string{"accessToken": "secret-response-token"})
				}
				panic("secret-panic-hash")
			}), &logs)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("POST", "https://example.com/", nil))
			if response.Code != 500 {
				t.Fatalf("success survived panic: %d", response.Code)
			}
			var result Error
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Code != "INTERNAL_ERROR" {
				t.Fatalf("not one JSON error: %q", response.Body.String())
			}
			if response.Header().Get("Set-Cookie") != "" || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("X-Request-ID") == "" {
				t.Fatal("unsafe or missing response headers")
			}
			for _, secret := range []string{"secret-header-token", "secret-response-token", "secret-panic-hash"} {
				if strings.Contains(response.Body.String()+response.Header().Get("Set-Cookie")+logs.String(), secret) {
					t.Fatal("secret survived recovery")
				}
			}
			var event map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil || event["status"] != float64(500) {
				t.Fatal("incorrect panic status log")
			}
		})
	}
}
