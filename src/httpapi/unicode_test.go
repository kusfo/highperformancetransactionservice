package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONUnicode(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		valid     bool
	}{
		{`"\ud800"`, "", false}, {`"\udfff"`, "", false}, {`"\ud800\ud800"`, "", false},
		{`"\udc00\ud800"`, "", false}, {`"\ud800x"`, "", false}, {`"\ud800\u0041"`, "", false},
		{`"\ud800\\udc00"`, "", false}, {`"\ud800\n\udc00"`, "", false},
		{`"\ud800\udc00"`, "𐀀", true}, {`"\uDBFF\uDFFF"`, "\U0010ffff", true},
		{`"\uD83D\uDE00"`, "😀", true}, {`"😀"`, "😀", true},
		{`"\ufffd"`, "�", true}, {`"�"`, "�", true}, {`"\\ud800"`, `\ud800`, true},
		{`"\\\ud800"`, "", false}, {`"\"\ud800"`, "", false},
		{`"a\u0000b"`, "a\x00b", true}, {`"  e\u0301  "`, "  é  ", true},
		{`"` + string([]byte{0xff}) + `"`, "", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"value":`+tc.raw+`}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			var out struct {
				Value string `json:"value"`
			}
			ok := DecodeJSON(w, r, &out)
			if ok != tc.valid {
				t.Fatalf("accepted=%v", ok)
			}
			if ok && out.Value != tc.want {
				t.Fatal("decoded text changed")
			}
			if !ok && (w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"INVALID_REQUEST"`)) {
				t.Fatal("wrong error")
			}
		})
	}
	// Invalid escapes in keys must also be rejected before key decoding.
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"\ud800":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	var out map[string]string
	if DecodeJSON(w, r, &out) || w.Code != 400 {
		t.Fatal("malformed key accepted")
	}
}
