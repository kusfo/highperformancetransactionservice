package userapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type Authenticator interface {
	Authenticate(context.Context, string, string) (sessions.Issued, error)
}

// NewHandlerWithLogin shares the two admission slots across registration/login.
func NewHandlerWithLogin(repository Creator, authenticator Authenticator) *Handler {
	h := NewHandler(repository)
	h.login = authenticator
	return h
}
func (h *Handler) serveLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpapi.WriteError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed.")
		return
	}
	var input loginRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	canonical, err := login.Validate(input.email, input.password)
	if err != nil {
		httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid login details.")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	case <-r.Context().Done():
		cancelled(w)
		return
	}
	if r.Context().Err() != nil {
		cancelled(w)
		return
	}
	issued, err := h.login.Authenticate(r.Context(), canonical, input.password)
	switch {
	case err == nil:
		httpapi.WriteJSON(w, 200, struct {
			AccessToken string    `json:"accessToken"`
			TokenType   string    `json:"tokenType"`
			ExpiresIn   int       `json:"expiresIn"`
			ExpiresAt   time.Time `json:"expiresAt"`
		}{issued.AccessToken(), "Bearer", 600, issued.Session.ExpiresAt})
	case errors.Is(err, login.ErrInvalidCredentials):
		httpapi.WriteError(w, 401, "INVALID_CREDENTIALS", "Invalid credentials.")
	case errors.Is(err, login.ErrInvalidInput):
		httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid login details.")
	case r.Context().Err() != nil:
		cancelled(w)
	default:
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
	}
}

type loginRequest struct{ registrationRequest }

func (r *loginRequest) UnmarshalJSON(data []byte) error {
	if err := r.registrationRequest.UnmarshalJSON(data); err != nil {
		return err
	}
	if r.displayName != nil {
		return users.ErrInvalidInput
	}
	return nil
}
