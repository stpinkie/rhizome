package qq

import (
	"net/http"
	"testing"
)

func TestFixQQBotAuthScheme_RewritesBearer(t *testing.T) {
	// Regression test for upstream sipeed/picoclaw#3365: resty >= v2.17 drops
	// the auth scheme botgo sets in OnBeforeRequest, so requests go out as
	// "Bearer <token>" and the QQ gateway 401s.
	req, err := http.NewRequest(http.MethodGet, "https://api.sgroup.qq.com/gateway/bot", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer abc123token")

	if err := fixQQBotAuthScheme(req, nil); err != nil {
		t.Fatalf("fixQQBotAuthScheme() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "QQBot abc123token" {
		t.Fatalf("Authorization = %q, want %q", got, "QQBot abc123token")
	}
}

func TestFixQQBotAuthScheme_LeavesOtherHeadersAlone(t *testing.T) {
	tests := []struct {
		name  string
		auth  string
		unset bool
		want  string
	}{
		{name: "no header", unset: true, want: ""},
		{name: "already QQBot", auth: "QQBot tok", want: "QQBot tok"},
		{name: "empty bearer", auth: "Bearer ", want: "Bearer "},
		{name: "other scheme", auth: "Basic Zm9v", want: "Basic Zm9v"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://api.sgroup.qq.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.unset {
				req.Header.Set("Authorization", tt.auth)
			}
			if err := fixQQBotAuthScheme(req, nil); err != nil {
				t.Fatalf("fixQQBotAuthScheme() error = %v", err)
			}
			if got := req.Header.Get("Authorization"); got != tt.want {
				t.Fatalf("Authorization = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterQQAuthSchemeFilter_Idempotent(t *testing.T) {
	RegisterQQAuthSchemeFilter()
	RegisterQQAuthSchemeFilter()
}
