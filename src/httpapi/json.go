package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

// Error is the public JSON error shape. Callers must use fixed safe messages,
// never driver errors, submitted values, passwords, hashes or access tokens.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Error{Code: code, Message: message})
}

// WriteJSON buffers encoding before writing headers to avoid partial JSON.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"code":"INTERNAL_ERROR","message":"An internal error occurred."}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

// DecodeJSON reads exactly one JSON object, rejecting unknown fields and
// malformed/trailing data without echoing submitted values. Use inside Runtime
// so MaxBytesReader bounds even chunked bodies; schema validation follows later.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		WriteError(w, 415, "UNSUPPORTED_MEDIA_TYPE", "Use an uncompressed application/json body.")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err = decoder.Decode(&raw); err != nil {
		decodeError(w, err)
		return false
	}
	var extra json.RawMessage
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple objects")
		}
		decodeError(w, err)
		return false
	}
	if len(raw) == 0 || raw[0] != '{' {
		WriteError(w, 400, "INVALID_REQUEST", "Request body must be a JSON object.")
		return false
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err = strict.Decode(dst); err != nil {
		decodeError(w, err)
		return false
	}
	return true
}

func decodeError(w http.ResponseWriter, err error) {
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		WriteError(w, 413, "REQUEST_TOO_LARGE", "Request body exceeds the configured limit.")
		return
	}
	WriteError(w, 400, "INVALID_REQUEST", "Request body is invalid.")
}
