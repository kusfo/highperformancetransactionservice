package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Runtime serves bounded JSON endpoints over TLS. Routes must respect context
// cancellation; the timeout wrapper cannot stop a handler that ignores it.
// Streaming, WebSocket and hijacking support are outside this JSON API runtime.
type Runtime struct {
	server *http.Server
	config Config
	logger *slog.Logger
}

func New(cfg Config, routes http.Handler, output io.Writer) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if routes == nil || output == nil {
		return nil, errors.New("handler and log output are required")
	}
	logger := slog.New(slog.NewJSONHandler(output, nil))
	safe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
			}
		}()
		routes.ServeHTTP(w, r)
	})
	timed := http.TimeoutHandler(safe, cfg.RequestTimeout, `{"code":"REQUEST_TIMEOUT","message":"Request processing timed out."}`+"\n")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
			return
		}
		w.Header().Set("X-Request-ID", hex.EncodeToString(id))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		record := &responseStatus{ResponseWriter: w}
		defer func() {
			status := record.status
			if status == 0 {
				status = 200
			}
			// Allowlist metadata: no URL/path/query, headers, bodies, IDs supplied by
			// clients, panic values, raw errors, or response payloads enter access logs.
			logger.Info("http_request", "request_id", hex.EncodeToString(id), "method", safeMethod(r.Method), "status", status, "duration_ms", time.Since(started).Milliseconds())
		}()
		if r.ContentLength > cfg.MaxBodyBytes {
			WriteError(record, 413, "REQUEST_TOO_LARGE", "Request body exceeds the configured limit.")
			return
		}
		r.Body = http.MaxBytesReader(record, r.Body, cfg.MaxBodyBytes)
		timed.ServeHTTP(record, r)
		// TimeoutHandler deliberately leaves cancellation responses empty. Add a
		// fixed JSON body without changing the already-written status header.
		if r.Context().Err() != nil && record.status == 503 && record.bytes == 0 {
			_, _ = record.Write([]byte(`{"code":"REQUEST_CANCELLED","message":"Request was cancelled."}` + "\n"))
		}
	})
	server := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
		MaxHeaderBytes: cfg.MaxHeaderBytes, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		// net/http diagnostic errors can contain client input: suppress their raw
		// text. Runtime emits fixed lifecycle messages and metadata-only access logs.
		ErrorLog: log.New(io.Discard, "", 0)}
	return &Runtime{server: server, config: cfg, logger: logger}, nil
}

type responseStatus struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseStatus) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseStatus) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}
func safeMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return method
	default:
		return "OTHER"
	}
}

// Run owns a listener and serves until cancellation or a server failure.
func (r *Runtime) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", r.config.Addr)
	if err != nil {
		return errors.New("HTTP listener failed")
	}
	return r.Serve(ctx, listener)
}

// Serve owns listener and closes it on all exit paths, including TLS setup
// failure. Cancellation drains active requests, then force-closes on timeout.
func (r *Runtime) Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cert, err := tls.LoadX509KeyPair(r.config.TLSCertFile, r.config.TLSKeyFile)
	if err != nil {
		return errors.New("HTTP TLS configuration failed")
	}
	r.server.TLSConfig.Certificates = []tls.Certificate{cert}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.server.BaseContext = func(net.Listener) context.Context { return base }
	done := make(chan error, 1)
	go func() { done <- r.server.ServeTLS(listener, "", "") }()
	r.logger.Info("http_started")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("HTTP serving failed")
	case <-ctx.Done():
		r.logger.Info("http_stopping")
		shutdown, stop := context.WithTimeout(context.Background(), r.config.ShutdownTimeout)
		err := r.server.Shutdown(shutdown)
		stop()
		if err != nil {
			cancel()
			_ = r.server.Close()
		}
		<-done
		if err != nil {
			r.logger.Warn("http_shutdown_timeout")
			return errors.New("HTTP shutdown timed out")
		}
		return nil
	}
}
