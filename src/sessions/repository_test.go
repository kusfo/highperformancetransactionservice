package sessions

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTokenParsingAndRedaction(t *testing.T) {
	for _, token := range []string{"", "secret-token", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("A", 42) + "B", strings.Repeat("!", 43), strings.Repeat("A", 42) + "\n"} {
		if _, err := tokenHash(token); err != ErrInvalidSession {
			t.Fatal("malformed token accepted")
		}
	}
	if _, err := tokenHash(strings.Repeat("A", 43)); err != nil {
		t.Fatal(err)
	}
	issued := Issued{token: "secret-access-token"}
	data, err := json.Marshal(issued)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{string(data), fmt.Sprint(issued), fmt.Sprintf("%+v", issued), fmt.Sprintf("%#v", &issued)} {
		if strings.Contains(s, issued.token) {
			t.Fatal("credential leaked")
		}
	}
	if issued.AccessToken() != issued.token {
		t.Fatal("explicit accessor failed")
	}
}
