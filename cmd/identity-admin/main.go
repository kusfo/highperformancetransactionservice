// Command identity-admin performs operator-only mutations with database access.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/identityadmin"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

var errInput = errors.New("invalid operator input")

// Input is deliberately never included in errors, flags, or output.
func parse(args []string, input io.Reader) (action, value string, err error) {
	if len(args) == 1 && args[0] == "revoke-session" {
		data, err := io.ReadAll(io.LimitReader(input, 46))
		if err != nil || len(data) > 45 {
			return "", "", errInput
		}
		token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\r\n"), "\n")
		raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
		if err != nil || len(token) != 43 || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
			return "", "", errInput
		}
		return args[0], token, nil
	}
	if len(args) == 2 && (args[0] == "enable-user" || args[0] == "revoke-user-sessions") && postgres.UUIDKey(args[1]).Valid {
		return args[0], args[1], nil
	}
	return "", "", errInput
}

func run(ctx context.Context, args []string, input io.Reader, url string) error {
	action, value, err := parse(args, input)
	if err != nil {
		return err
	}
	if url == "" {
		return errors.New("database configuration required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return errors.New("database connection failed")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	switch action {
	case "revoke-session":
		return sessions.NewRepository(conn, nil).Revoke(ctx, value)
	case "enable-user":
		return identityadmin.NewService(conn).Enable(ctx, value)
	default:
		return identityadmin.NewService(conn).RevokeUserSessions(ctx, value)
	}
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--help" {
		fmt.Println("Usage: identity-admin revoke-session < token-file | revoke-user-sessions USER_UUID | enable-user USER_UUID\nRequires operator DATABASE_URL. Never pass tokens as arguments.")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Getenv("DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, "identity administration failed; check command, input, user and database configuration")
		os.Exit(1)
	}
	fmt.Println("identity administration completed")
}
