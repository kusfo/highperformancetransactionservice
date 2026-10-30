package userapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kusfo/highperformancetransactionservice/src/authn"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type ProfileEditor interface {
	Update(context.Context, string, string) (users.User, error)
	ChangePassword(context.Context, string, string, string) error
}

// NewHandlerWithProfile adds protected profile routes. Password changes share
// registration/login's admission limit to bound Argon2 memory per process.
func NewHandlerWithProfile(repository Creator, authenticator Authenticator, checker authn.Checker, editor ProfileEditor) *Handler {
	h := NewHandlerWithLogin(repository, authenticator)
	h.protected = authn.Require(checker, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.serveProfile(w, r, editor) }))
	return h
}
func (h *Handler) serveProfile(w http.ResponseWriter, r *http.Request, editor ProfileEditor) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/users/"), "/")
	password := len(parts) == 2 && parts[1] == "password"
	method := "PATCH"
	if password {
		method = "PUT"
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		httpapi.WriteError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed.")
		return
	}
	identity, ok := authn.FromContext(r.Context())
	if !ok || parts[0] != identity.UserID {
		httpapi.WriteError(w, 404, "NOT_FOUND", "Resource not found.")
		return
	}
	input := exactStrings{allowed: []string{"displayName"}}
	if password {
		input.allowed = []string{"currentPassword", "newPassword"}
	}
	if !httpapi.DecodeJSON(w, r, &input) {
		return
	}
	var result users.User
	var err error
	if password {
		if profile.ValidatePasswordChange(input.values["currentPassword"], input.values["newPassword"]) != nil {
			httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid password details.")
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
		err = editor.ChangePassword(r.Context(), identity.UserID, input.values["currentPassword"], input.values["newPassword"])
	} else {
		name := input.values["displayName"]
		if users.ValidateDisplayName(&name) != nil {
			httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid profile details.")
			return
		}
		result, err = editor.Update(r.Context(), identity.UserID, name)
	}
	switch {
	case err == nil:
		if password {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(204)
		} else {
			httpapi.WriteJSON(w, 200, result)
		}
	case errors.Is(err, profile.ErrCurrentPassword):
		httpapi.WriteError(w, 403, "INVALID_CURRENT_PASSWORD", "Invalid current password.")
	case errors.Is(err, users.ErrNotFound):
		httpapi.WriteError(w, 404, "NOT_FOUND", "Resource not found.")
	case errors.Is(err, users.ErrInvalidInput):
		httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid update details.")
	case r.Context().Err() != nil:
		cancelled(w)
	default:
		httpapi.WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
	}
}

// exactStrings rejects omitted, null, unknown, duplicate and mis-cased fields.
type exactStrings struct {
	allowed []string
	values  map[string]string
}

func (s *exactStrings) UnmarshalJSON(data []byte) error {
	if !utf8.Valid(data) {
		return users.ErrInvalidInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return users.ErrInvalidInput
	}
	s.values = make(map[string]string)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return users.ErrInvalidInput
		}
		key, ok := token.(string)
		if !ok {
			return users.ErrInvalidInput
		}
		allowed := false
		for _, field := range s.allowed {
			if key == field {
				allowed = true
			}
		}
		if _, seen := s.values[key]; seen || !allowed {
			return users.ErrInvalidInput
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '"' {
			return users.ErrInvalidInput
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return users.ErrInvalidInput
		}
		s.values[key] = value
	}
	if _, err = d.Token(); err != nil || len(s.values) != len(s.allowed) {
		return users.ErrInvalidInput
	}
	return nil
}
