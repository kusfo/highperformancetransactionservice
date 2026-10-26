//go:build integration

package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/tests/testdb"
)

func buildCLI(t *testing.T, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), name)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", path, "./cmd/"+name)
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v %s", name, err, output)
	}
	return path
}
func cliEnv(values map[string]string) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "HTTP_") || strings.HasPrefix(key, "PG") || key == "DATABASE_URL" {
			continue
		}
		if _, ok := values[key]; !ok {
			env = append(env, item)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}
func runCLI(t *testing.T, path string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = cliEnv(env)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
func TestMigrationCLI(t *testing.T) {
	path := buildCLI(t, "migrate")
	t.Run("missing_url", func(t *testing.T) {
		output, err := runCLI(t, path, nil)
		if err == nil || !strings.Contains(output, "migration failed") {
			t.Fatalf("expected configuration failure: %v %s", err, output)
		}
	})
	t.Run("invalid_url_redaction", func(t *testing.T) {
		output, err := runCLI(t, path, map[string]string{"DATABASE_URL": "postgres://user:secret-token@invalid.invalid/db?bad=%"})
		if err == nil || strings.Contains(output, "secret-token") || strings.Contains(output, "invalid.invalid") {
			t.Fatalf("unsafe diagnostics: %v %s", err, output)
		}
	})
	for _, mode := range []string{"rerun", "empty", "sql_failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			conn := testdb.New(t)
			ctx := testContext(t)
			dir := t.TempDir()
			databaseURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
			if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") {
				t.Fatal("CLI tests require a PostgreSQL URL")
			}
			databaseURL.Path = "/" + conn.Config().Database
			databaseURL.RawPath = ""
			query := databaseURL.Query()
			query.Del("dbname")
			databaseURL.RawQuery = query.Encode()
			sql := "CREATE TABLE cli_fixture(id integer); INSERT INTO cli_fixture VALUES(1);"
			switch mode {
			case "empty":
				sql = ""
			case "sql_failure":
				sql = "CREATE TABLE cli_fixture(id integer); DO $$ BEGIN RAISE EXCEPTION 'secret-password-hash-token'; END $$;"
			case "timeout":
				sql = "CREATE TABLE cli_fixture(id integer); SELECT pg_sleep(5);"
			}
			if sql != "" {
				if err := os.WriteFile(filepath.Join(dir, "000001_cli.sql"), []byte(sql), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-dir", dir}
			if mode == "timeout" {
				args = append(args, "-timeout", "100ms")
			}
			output, err := runCLI(t, path, map[string]string{"DATABASE_URL": databaseURL.String()}, args...)
			if mode == "sql_failure" || mode == "timeout" {
				if err == nil || strings.Contains(output, "secret-password-hash-token") {
					t.Fatalf("expected safe failure: %v %s", err, output)
				}
				var exists bool
				if err := conn.QueryRow(ctx, `SELECT to_regclass('cli_fixture') IS NOT NULL OR to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil || exists {
					t.Fatalf("CLI partial state: %v %v", exists, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("migration failed: %v %s", err, output)
			}
			if output, err = runCLI(t, path, map[string]string{"DATABASE_URL": databaseURL.String()}, args...); err != nil {
				t.Fatalf("rerun: %v %s", err, output)
			}
			var count int
			expected := 1
			if mode == "empty" {
				expected = 0
			}
			if err := conn.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil || count != expected {
				t.Fatalf("history: %d %v", count, err)
			}
			if mode == "rerun" {
				if err := conn.QueryRow(ctx, `SELECT count(*) FROM cli_fixture`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("duplicated CLI migration: %d %v", count, err)
				}
			}
		})
	}
}

func TestFixtureFailureChild(t *testing.T) {
	if os.Getenv("HPTS_FIXTURE_FAILURE_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	conn := testdb.New(t)
	var name string
	if err := conn.QueryRow(context.Background(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	fmt.Println("FIXTURE_DB=" + name)
	t.Fatal("intentional fixture failure")
}
func TestFixtureCleanupAfterTestFailure(t *testing.T) {
	ctx := testContext(t)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFixtureFailureChild$", "-test.v")
	cmd.Env = append(os.Environ(), "HPTS_FIXTURE_FAILURE_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("child must fail")
	}
	var name string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "FIXTURE_DB=") {
			name = strings.TrimPrefix(line, "FIXTURE_DB=")
		}
	}
	if !strings.HasPrefix(name, "t06_") {
		t.Fatalf("child did not create fixture: %s", output)
	}
	admin, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); err != nil || exists {
		t.Fatalf("failed test leaked fixture: %v %v", exists, err)
	}
}

func serviceCertificate(t *testing.T) (string, string, *http.Client) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(cert)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(client.CloseIdleConnections)
	return certFile, keyFile, client
}
func TestServiceBinaries(t *testing.T) {
	for _, name := range []string{"user", "account", "transaction"} {
		t.Run(name, func(t *testing.T) {
			path := buildCLI(t, name)
			if output, err := runCLI(t, path, nil); err == nil || !strings.Contains(output, "configuration") {
				t.Fatalf("missing TLS did not fail: %v %s", err, output)
			}
			cert, key, client := serviceCertificate(t)
			reserved, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := reserved.Addr().String()
			_ = reserved.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, path)
			env := map[string]string{"HTTP_ADDR": addr, "HTTP_TLS_CERT_FILE": cert, "HTTP_TLS_KEY_FILE": key}
			if name == "user" {
				conn, _, _ := userFixture(t)
				databaseURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
				if err != nil {
					t.Fatal(err)
				}
				databaseURL.Path = "/" + conn.Config().Database
				databaseURL.RawPath = ""
				env["DATABASE_URL"] = databaseURL.String()
				for _, badURL := range []string{"", "postgres://user:secret-token@invalid.invalid/db?bad=%"} {
					badEnv := map[string]string{"HTTP_TLS_CERT_FILE": cert, "HTTP_TLS_KEY_FILE": key, "DATABASE_URL": badURL}
					if output, err := runCLI(t, path, badEnv); err == nil || strings.Contains(output, "secret-token") {
						t.Fatal("database configuration was not safely rejected")
					}
				}
			}
			cmd.Env = cliEnv(env)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			ready := false
			for !ready {
				response, err := client.Get("https://" + addr + "/not-implemented")
				if err == nil {
					_ = response.Body.Close()
					if response.StatusCode != 404 {
						t.Fatalf("unexpected service status: %d", response.StatusCode)
					}
					ready = true
					break
				}
				select {
				case err := <-done:
					t.Fatalf("service exited early: %v", err)
				case <-ctx.Done():
					t.Fatal("service startup timeout")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if name == "user" {
				response, err := client.Post("https://"+addr+"/users", "application/json", strings.NewReader(`{"email":"binary@example.com","password":"example-password"}`))
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != 201 || !strings.Contains(string(body), "userId") {
					t.Fatal("binary registration failed")
				}
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("unclean SIGTERM: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("SIGTERM shutdown timeout")
			}
			if conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
				_ = conn.Close()
				t.Fatal("service listener remained open")
			}
		})
	}
}
