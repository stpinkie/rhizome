//go:build darwin

package isolation

import (
	"strings"
	"testing"
)

func TestBuildDarwinSandboxProfile_NetMode(t *testing.T) {
	userEnv := ResolveUserEnv("/root")

	profile, err := buildDarwinSandboxProfile(nil, "/root", userEnv, "")
	if err != nil {
		t.Fatalf("buildDarwinSandboxProfile() error = %v", err)
	}
	if !strings.Contains(profile, "(allow network-outbound)") {
		t.Fatal("default profile must allow network-outbound")
	}

	profile, err = buildDarwinSandboxProfile(nil, "/root", userEnv, NetModeInherit)
	if err != nil {
		t.Fatalf("buildDarwinSandboxProfile() error = %v", err)
	}
	if !strings.Contains(profile, "(allow network-outbound)") {
		t.Fatal("inherit profile must allow network-outbound")
	}

	profile, err = buildDarwinSandboxProfile(nil, "/root", userEnv, NetModeNone)
	if err != nil {
		t.Fatalf("buildDarwinSandboxProfile() error = %v", err)
	}
	if strings.Contains(profile, "network") {
		t.Fatalf("net_mode=none profile must not mention network rules:\n%s", profile)
	}
}
