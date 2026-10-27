// Package userapi implements the user HTTP contract on top of identity storage.
package userapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type Creator interface {
	Create(context.Context, string, string, *string) (users.User, error)
}

type Handler struct {
	users Creator
	login Authenticator
	slots chan struct{}
	hash  func(string) (string, error)
}

// NewHandler bounds active registrations (including hashing and storage) to two
// per process. Waiting requests respect Runtime's request deadline.
func NewHandler(repository Creator) *Handler {
	return &Handler{users: repository, slots: make(chan struct{}, 2), hash: passwords.Hash}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/sessions" && h.login != nil {
		h.serveLogin(w, r)
		return
	}
	if r.URL.Path != "/users" {
		httpapi.WriteError(w, 404, "NOT_FOUND", "Resource not found.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpapi.WriteError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed.")
		return
	}
	var input registrationRequest
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	email, err := users.NormalizeEmail(input.email)
	if err != nil || passwords.Validate(input.password) != nil || users.ValidateDisplayName(input.displayName) != nil {
		httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid registration details.")
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
	hash, err := h.hash(input.password)
	if err != nil {
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
		return
	}
	// Argon2 cannot be interrupted. Never persist after noticing cancellation.
	if r.Context().Err() != nil {
		cancelled(w)
		return
	}
	user, err := h.users.Create(r.Context(), email, hash, input.displayName)
	switch {
	case err == nil:
		httpapi.WriteJSON(w, 201, struct {
			UserID string `json:"userId"`
		}{user.UserID})
	case errors.Is(err, users.ErrEmailAlreadyUsed):
		httpapi.WriteError(w, 409, "EMAIL_ALREADY_USED", "Email is already registered.")
	case errors.Is(err, users.ErrInvalidInput):
		httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid registration details.")
	case r.Context().Err() != nil:
		cancelled(w)
	default:
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
	}
}

func cancelled(w http.ResponseWriter) {
	httpapi.WriteError(w, 503, "REQUEST_CANCELLED", "Request was cancelled.")
}

// Decode exact field names and string types. Reject null, duplicate fields and
// unknown fields instead of encoding/json's permissive struct matching.
type registrationRequest struct {
	email, password string
	displayName     *string
}

func (r *registrationRequest) UnmarshalJSON(data []byte) error {
	if !utf8.Valid(data) {
		return users.ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return users.ErrInvalidInput
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return users.ErrInvalidInput
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return users.ErrInvalidInput
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '"' {
			return users.ErrInvalidInput
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return users.ErrInvalidInput
		}
		switch key {
		case "email":
			r.email = value
		case "password":
			r.password = value
		case "displayName":
			r.displayName = &value
		default:
			return users.ErrInvalidInput
		}
	}
	if _, err := decoder.Token(); err != nil || !seen["email"] || !seen["password"] {
		return users.ErrInvalidInput
	}
	return nil
}
