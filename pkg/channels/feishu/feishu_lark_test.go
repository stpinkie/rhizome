//go:build feishu && (amd64 || arm64 || riscv64 || mips64 || ppc64)

package feishu

import (
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestStripMentionPlaceholders(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name     string
		content  string
		mentions []*larkim.MentionEvent
		want     string
	}{
		{
			name:     "no mentions",
			content:  "Hello world",
			mentions: nil,
			want:     "Hello world",
		},
		{
			name:    "single mention",
			content: "@_user_1 hello",
			mentions: []*larkim.MentionEvent{
				{Key: strPtr("@_user_1")},
			},
			want: "hello",
		},
		{
			name:    "multiple mentions",
			content: "@_user_1 @_user_2 hey",
			mentions: []*larkim.MentionEvent{
				{Key: strPtr("@_user_1")},
				{Key: strPtr("@_user_2")},
			},
			want: "hey",
		},
		{
			name:     "empty content",
			content:  "",
			mentions: []*larkim.MentionEvent{{Key: strPtr("@_user_1")}},
			want:     "",
		},
		{
			name:     "empty mentions slice",
			content:  "@_user_1 test",
			mentions: []*larkim.MentionEvent{},
			want:     "@_user_1 test",
		},
		{
			name:    "mention with nil key",
			content: "@_user_1 test",
			mentions: []*larkim.MentionEvent{
				{Key: nil},
			},
			want: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMentionPlaceholders(tt.content, tt.mentions)
			if got != tt.want {
				t.Errorf("stripMentionPlaceholders(%q, ...) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
