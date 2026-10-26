//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/userapi"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type safeLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *safeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *safeLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

func TestRegistrationHTTPSPersistence(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	pc, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.Database = conn.Config().Database
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := httpapi.DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	cert, key, client := serviceCertificate(t)
	client.Timeout = 10 * time.Second
	cfg.TLSCertFile = cert
	cfg.TLSKeyFile = key
	logs := &safeLog{}
	runtime, err := httpapi.New(cfg, userapi.NewHandler(users.NewRepository(pool)), logs)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(serverCtx, listener) }()
	stopServer := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(12 * time.Second):
			t.Error("server cleanup timeout")
		}
	})
	t.Cleanup(stopServer)
	endpoint := "https://" + listener.Addr().String() + "/users"
	submit := func(body string) (int, string, error) {
		response, err := client.Post(endpoint, "application/json", strings.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if response.Header.Get("Cache-Control") != "no-store" {
			return response.StatusCode, "", io.ErrUnexpectedEOF
		}
		return response.StatusCode, string(data), err
	}
	password := " example-password界 "
	data, _ := json.Marshal(map[string]string{"email": " Person@Example.COM ", "password": password, "displayName": strings.Repeat("界", 100)})
	status, body, err := submit(string(data))
	if err != nil || status != 201 {
		t.Fatalf("registration: %d %v", status, err)
	}
	var result map[string]string
	if json.Unmarshal([]byte(body), &result) != nil || len(result) != 1 || len(result["userId"]) != 36 {
		t.Fatal("incorrect public result")
	}
	c, err := repo.CredentialsByEmail(ctx, "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.User.UserID != result["userId"] || c.User.DisplayName != strings.Repeat("界", 100) || !c.User.Active {
		t.Fatal("profile mismatch")
	}
	if ok, err := passwords.Verify(password, c.PasswordHash()); err != nil || !ok {
		t.Fatal("stored hash does not verify")
	}
	status, _, err = submit(string(data))
	if err != nil || status != 409 {
		t.Fatal("duplicate not rejected")
	}
	if err := repo.SetActive(ctx, c.User.UserID, false); err != nil {
		t.Fatal(err)
	}
	status, _, err = submit(string(data))
	if err != nil || status != 409 {
		t.Fatal("inactive email reused")
	}
	// The HTTP endpoint and pool must rely on real database uniqueness under concurrency.
	start := make(chan struct{})
	results := make(chan int, 4)
	for i := 0; i < 4; i++ {
		email := []string{"race@example.com", " RACE@EXAMPLE.COM "}[i%2]
		go func() {
			<-start
			b, _ := json.Marshal(map[string]string{"email": email, "password": password})
			s, _, err := submit(string(b))
			if err != nil {
				s = 0
			}
			results <- s
		}()
	}
	close(start)
	success := 0
	for i := 0; i < 4; i++ {
		s := <-results
		if s == 201 {
			success++
		} else if s != 409 {
			t.Fatalf("unexpected concurrency status %d", s)
		}
	}
	if success != 1 {
		t.Fatalf("created %d users", success)
	}
	race, err := repo.CredentialsByEmail(ctx, "race@example.com")
	if err != nil || race.User.DisplayName != "" {
		t.Fatal("omitted name not empty")
	}
	if race.PasswordHash() == c.PasswordHash() {
		t.Fatal("salt reused")
	}
	status, body, err = submit(`{"email":"invalid@example.com","password":"example-password","saltedhashedpassword":"client-secret-hash"}`)
	if err != nil || status != 400 {
		t.Fatal("client hash accepted")
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM identity.users").Scan(&count); err != nil || count != 2 {
		t.Fatalf("unexpected row count %d: %v", count, err)
	}
	pool.Close()
	status, body, err = submit(`{"email":"outage@example.com","password":"example-password"}`)
	if err != nil || status != 500 || !strings.Contains(body, "INTERNAL_ERROR") {
		t.Fatal("storage error not mapped")
	}
	stopServer()
	for _, secret := range []string{password, "example-password", "client-secret-hash", c.PasswordHash(), race.PasswordHash(), "person@example.com"} {
		if strings.Contains(logs.String(), secret) || strings.Contains(body, secret) {
			t.Fatal("credential or input leaked")
		}
	}
}
