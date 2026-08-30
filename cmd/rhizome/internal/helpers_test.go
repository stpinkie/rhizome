package internal

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
)

func TestGetConfigPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/home")
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "/tmp/home")
	}

	got := GetConfigPath()
	want := filepath.Join("/tmp/home", ".rhizome", "config.json")

	assert.Equal(t, want, got)
}

func TestGetConfigPath_WithRHIZOME_HOME(t *testing.T) {
	t.Setenv(config.EnvHome, "/custom/rhizome")
	t.Setenv("HOME", "/tmp/home")
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "/tmp/home")
	}

	got := GetConfigPath()
	want := filepath.Join("/custom/rhizome", "config.json")

	assert.Equal(t, want, got)
}

func TestGetConfigPath_WithRHIZOME_CONFIG(t *testing.T) {
	t.Setenv("RHIZOME_CONFIG", "/custom/config.json")
	t.Setenv(config.EnvHome, "/custom/rhizome")
	t.Setenv("HOME", "/tmp/home")
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "/tmp/home")
	}

	got := GetConfigPath()
	want := "/custom/config.json"

	assert.Equal(t, want, got)
}

func TestGetConfigPath_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific HOME behavior varies; run on windows")
	}

	testUserProfilePath := `C:\Users\Test`
	t.Setenv("USERPROFILE", testUserProfilePath)

	got := GetConfigPath()
	want := filepath.Join(testUserProfilePath, ".rhizome", "config.json")

	require.True(t, strings.EqualFold(got, want), "GetConfigPath() = %q, want %q", got, want)
}
