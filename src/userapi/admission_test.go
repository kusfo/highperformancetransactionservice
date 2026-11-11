package userapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type admissionEditor struct{ enter func(string) }

func (e admissionEditor) Update(context.Context, string, string) (users.User, error) {
	return users.User{}, nil
}
func (e admissionEditor) ChangePassword(context.Context, string, string, string) error {
	e.enter("password")
	return nil
}

// Signals when the handler evaluates its admission select. This avoids assuming
// a goroutine has reached the gate merely because it was launched.
type observedAdmissionContext struct {
	context.Context
	once    sync.Once
	reached chan struct{}
}

func (c *observedAdmissionContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}

func TestSharedCredentialAdmissionAndCancellation(t *testing.T) {
	for _, waitingKind := range []string{"password", "login", "registration"} {
		t.Run(waitingKind, func(t *testing.T) {
			entered := make(chan string, 4)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			enter := func(kind string) { entered <- kind; <-release }
			h := NewHandlerWithProfile(createFunc(func(context.Context, string, string, *string) (users.User, error) { return users.User{}, nil }), authFunc(func(context.Context, string, string) (sessions.Issued, error) {
				enter("login")
				return sessions.Issued{}, nil
			}), profileChecker{}, admissionEditor{enter})
			h.hash = func(string) (string, error) { enter("registration"); return "fixture-hash", nil }
			run := func(kind string, ctx context.Context, done chan<- int) {
				method, path, body := "POST", "/users", validBody
				if kind == "login" {
					path = "/sessions"
				}
				if kind == "password" {
					method = "PUT"
					path = "/users/owner/password"
					body = `{"currentPassword":"old-password","newPassword":"new-password"}`
				}
				r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer fixture")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				done <- w.Code
			}
			activeDone := make(chan int, 2)
			for _, kind := range []string{"registration", "login", "password"} {
				if kind != waitingKind {
					go run(kind, context.Background(), activeDone)
				}
			}
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("active request did not enter")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := &observedAdmissionContext{Context: ctx, reached: make(chan struct{})}
			waitDone := make(chan int, 1)
			go run(waitingKind, observed, waitDone)
			select {
			case <-observed.reached:
			case <-time.After(3 * time.Second):
				t.Fatal("waiter did not reach admission")
			}
			cancel()
			select {
			case status := <-waitDone:
				if status != 503 {
					t.Fatalf("cancelled waiter status=%d", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled waiter remained blocked")
			}
			select {
			case <-entered:
				t.Fatal("third credential operation bypassed shared admission")
			default:
			}
			unblock()
			for i := 0; i < 2; i++ {
				select {
				case status := <-activeDone:
					if status >= 400 {
						t.Fatalf("active operation status=%d", status)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("active operation did not finish")
				}
			}
			if len(h.slots) != 0 {
				t.Fatal("admission slot leaked")
			}
			retry := make(chan int, 1)
			go run(waitingKind, context.Background(), retry)
			select {
			case status := <-retry:
				if status >= 400 {
					t.Fatal("subsequent operation failed")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("released slots not reusable")
			}
			if len(h.slots) != 0 {
				t.Fatal("retry leaked admission slot")
			}
		})
	}
}
