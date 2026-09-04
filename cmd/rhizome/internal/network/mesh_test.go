package network

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

func TestNewSavedPeersCommandListsSavedPeers(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	t.Setenv(config.EnvConfig, configPath)

	cmd := NewSavedPeersCommand()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buf.String()
	if !contains(output, peerID) {
		t.Fatalf("output missing peer id: %s", output)
	}
	if !contains(output, `"trusted": true`) {
		t.Fatalf("output missing trusted flag: %s", output)
	}
}

func TestNewRemoveCommandRemovesPeerFromConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	t.Setenv(config.EnvConfig, configPath)

	cmd := NewRemoveCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{peerID})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(saved.Mesh.TrustedPeers) != 0 {
		t.Fatalf("trusted peers = %v, want empty", saved.Mesh.TrustedPeers)
	}
	if len(saved.Mesh.BootstrapPeers) != 0 {
		t.Fatalf("bootstrap peers = %v, want empty", saved.Mesh.BootstrapPeers)
	}
}

func TestNewRemoveCommandReportsMissingPeer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(configPath, config.DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	t.Setenv(config.EnvConfig, configPath)

	cmd := NewRemoveCommand()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{peerID})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !contains(buf.String(), "is not in saved peers") {
		t.Fatalf("expected 'is not in saved peers' message, got: %s", buf.String())
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
