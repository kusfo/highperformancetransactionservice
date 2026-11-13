// Package accountapi implements authenticated account HTTP entry points.
package accountapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/kusfo/highperformancetransactionservice/src/authn"
	"github.com/kusfo/highperformancetransactionservice/src/financial"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
)

type Creator interface {
	Create(context.Context, string, financial.Metadata) (financial.Account, error)
}

// NewHandler authenticates before decoding input or creating an account.
// Creation intentionally has no replay guarantee.
func NewHandler(accounts Creator, checker authn.Checker) http.Handler {
	create := authn.Require(checker, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			httpapi.WriteError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed.")
			return
		}
		if _, present := r.Header[http.CanonicalHeaderKey("Idempotency-Key")]; present {
			httpapi.WriteError(w, 400, "INVALID_REQUEST", "Account creation does not support Idempotency-Key.")
			return
		}
		var input createRequest
		if !httpapi.DecodeJSON(w, r, &input) {
			return
		}
		if financial.ValidateMetadata(input.Metadata) != nil {
			httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid account metadata.")
			return
		}
		if r.Context().Err() != nil {
			httpapi.WriteError(w, 503, "REQUEST_CANCELLED", "Request was cancelled.")
			return
		}
		identity, _ := authn.FromContext(r.Context())
		account, err := accounts.Create(r.Context(), identity.UserID, input.Metadata)
		switch {
		case err == nil:
			w.Header().Set("Location", "/accounts/"+url.PathEscape(account.AccountNumber))
			httpapi.WriteJSON(w, 201, account)
		case r.Context().Err() != nil:
			httpapi.WriteError(w, 503, "REQUEST_CANCELLED", "Request was cancelled.")
		case errors.Is(err, financial.ErrInvalidInput):
			httpapi.WriteError(w, 400, "INVALID_REQUEST", "Invalid account metadata.")
		default:
			httpapi.WriteError(w, 500, "INTERNAL_ERROR", "An internal error occurred.")
		}
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts" {
			httpapi.WriteError(w, 404, "NOT_FOUND", "Resource not found.")
			return
		}
		create.ServeHTTP(w, r)
	})
}

type createRequest struct{ financial.Metadata }

// Decode exact, unique field names and string values, including optional empty
// strings. DecodeJSON checks syntax and Unicode before invoking this method.
func (r *createRequest) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return financial.ErrInvalidInput
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return financial.ErrInvalidInput
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return financial.ErrInvalidInput
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '"' {
			return financial.ErrInvalidInput
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return financial.ErrInvalidInput
		}
		switch key {
		case "displayName":
			r.DisplayName = value
		case "description":
			r.Description = value
		default:
			return financial.ErrInvalidInput
		}
	}
	_, err = decoder.Token()
	return err
}
