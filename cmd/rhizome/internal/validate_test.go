package internal

import (
	"testing"
)

func TestValidatePeerID(t *testing.T) {
	if err := ValidatePeerID("12D3KooWRmC7mvWD1cc6RbDzA1h6m7g68Y3R8j1o0S1F9J2L3M4N5"); err == nil {
		t.Fatal("expected error for invalid peer id")
	}
}

func TestValidateMultiaddrWithPeerID(t *testing.T) {
	if err := ValidateMultiaddrWithPeerID("/ip4/127.0.0.1/tcp/1234"); err == nil {
		t.Fatal("expected error for multiaddr without peer id")
	}
	if err := ValidateMultiaddrWithPeerID("not-a-multiaddr"); err == nil {
		t.Fatal("expected error for invalid multiaddr")
	}
}

func TestValidateAgentID(t *testing.T) {
	cases := []struct {
		input string
		valid bool
	}{
		{"main", true},
		{"Main", true},
		{"agent-1", true},
		{"agent_2", true},
		{"", false},
		{"agent with spaces", false},
		{"agent:invalid", false},
		{"-leading", false},
	}
	for _, c := range cases {
		got, err := ValidateAgentID(c.input)
		if c.valid && err != nil {
			t.Fatalf("ValidateAgentID(%q) expected valid, got error: %v", c.input, err)
		}
		if !c.valid && err == nil {
			t.Fatalf("ValidateAgentID(%q) expected invalid, got %q", c.input, got)
		}
	}
}

func TestValidateSwarmID(t *testing.T) {
	if err := ValidateSwarmID("ops"); err != nil {
		t.Fatalf("expected 'ops' to be valid: %v", err)
	}
	if err := ValidateSwarmID(""); err == nil {
		t.Fatal("expected error for empty swarm id")
	}
	if err := ValidateSwarmID("bad swarm"); err == nil {
		t.Fatal("expected error for swarm id with spaces")
	}
}

func TestValidateScatterStrategy(t *testing.T) {
	for _, s := range []string{"first", "quorum", "all"} {
		got, err := ValidateScatterStrategy(s)
		if err != nil {
			t.Fatalf("ValidateScatterStrategy(%q) error: %v", s, err)
		}
		if got != s {
			t.Fatalf("ValidateScatterStrategy(%q) = %q, want %q", s, got, s)
		}
	}
	if got, err := ValidateScatterStrategy("FIRST"); err != nil || got != "first" {
		t.Fatalf("ValidateScatterStrategy(\"FIRST\") = %q, %v, want first", got, err)
	}
	if _, err := ValidateScatterStrategy("unknown"); err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}
