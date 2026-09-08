package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMask_BearerToken(t *testing.T) {
	got := Mask("request failed: Bearer abcdef1234567890XYZW")
	if !strings.Contains(got, "abcd****") {
		t.Fatalf("expected masked bearer token, got %q", got)
	}
	if strings.Contains(got, "abcdef1234567890XYZW") {
		t.Fatalf("token leaked: %q", got)
	}
}

func TestMask_APIKey(t *testing.T) {
	got := Mask(`config: api_key=sk_live_abcdef123456`)
	if strings.Contains(got, "sk_live_abcdef123456") {
		t.Fatalf("api key leaked: %q", got)
	}
}

func TestMask_SecretPrefix(t *testing.T) {
	got := Mask("key is sk-abcdef1234567890wxyz")
	if strings.Contains(got, "abcdef1234567890") {
		t.Fatalf("sk- secret leaked: %q", got)
	}
	if !strings.Contains(got, "sk-") {
		t.Fatalf("prefix should be preserved: %q", got)
	}
}

func TestMask_TelegramToken(t *testing.T) {
	got := Mask("bot token: bot123456789:AAEXAMPLETOKEN1234567890abcdefg")
	if strings.Contains(got, "AAEXAMPLETOKEN1234567890") {
		t.Fatalf("telegram token leaked: %q", got)
	}
}

func TestMask_AuthorizationHeader(t *testing.T) {
	got := Mask("Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9")
	if strings.Contains(got, "eyJhbGciOiJIUzI1NiIsInR5cCI6") {
		t.Fatalf("authorization token leaked: %q", got)
	}
}

func TestMask_EmptyAndPlain(t *testing.T) {
	if got := Mask(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	plain := "normal log message without secrets"
	if got := Mask(plain); got != plain {
		t.Fatalf("plain text altered: %q", got)
	}
}

func TestMask_ExtraMasker(t *testing.T) {
	SetExtraMasker(func(s string) string {
		return strings.ReplaceAll(s, "my-secret-value", "[FILTERED]")
	})
	defer SetExtraMasker(nil)

	got := Mask("the configured key is my-secret-value ok")
	if strings.Contains(got, "my-secret-value") {
		t.Fatalf("configured secret leaked: %q", got)
	}
	if !strings.Contains(got, "[FILTERED]") {
		t.Fatalf("extra masker not applied: %q", got)
	}
}

func TestMaskAny_MapStringAny(t *testing.T) {
	in := map[string]any{
		"command": `curl -H "Authorization: Bearer abcdef1234567890XYZW" https://x`,
		"api_key": "hunter2-not-a-known-pattern",
		"nested":  map[string]any{"token": "abcdef1234567890wxyz"},
		"list":    []any{"sk-abcdef1234567890wxyz", "plain"},
		"n":       42,
	}
	out, ok := MaskAny(in).(map[string]any)
	if !ok {
		t.Fatalf("MaskAny(map) returned %T", MaskAny(in))
	}
	cmd := out["command"].(string)
	if strings.Contains(cmd, "abcdef1234567890") {
		t.Fatalf("bearer token leaked in nested string: %q", cmd)
	}
	if out["api_key"] != FilteredPlaceholder {
		t.Fatalf("secretish key not redacted: %v", out["api_key"])
	}
	nested := out["nested"].(map[string]any)
	if nested["token"] != FilteredPlaceholder {
		t.Fatalf("nested secretish key not redacted: %v", nested["token"])
	}
	list := out["list"].([]any)
	if strings.Contains(list[0].(string), "abcdef1234567890") {
		t.Fatalf("list element leaked: %v", list)
	}
	if list[1] != "plain" || out["n"] != 42 {
		t.Fatalf("non-secret values altered: %v", out)
	}
}

func TestMaskAny_FallbackPreservesJSON(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Token string `json:"auth_token"`
	}
	out := MaskAny(payload{Name: "x", Token: "abcdef1234567890wxyz"})
	raw, ok := out.(json.RawMessage)
	if !ok {
		t.Fatalf("struct fallback = %T, want json.RawMessage", out)
	}
	s := string(raw)
	if strings.Contains(s, "abcdef1234567890") {
		t.Fatalf("secret in struct field leaked: %s", s)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("masked JSON invalid: %s (%v)", s, err)
	}
	if decoded["auth_token"] != FilteredPlaceholder {
		t.Fatalf("secretish JSON key not redacted: %s", s)
	}
}
