//go:build integration

// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package acp

// Track 97 live acceptance: a real ACP task over `docker run -i --rm` stdio.
// Runs inside the acp-container integration suite, where the runner mounts
// the host docker socket and run.sh pre-builds the fixture image.

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
)

func TestIntegrationACPContainerRuntime(t *testing.T) {
	if os.Getenv("RHIZOME_ACP_CONTAINER_IT") != "1" {
		t.Skip("acp-container integration suite only (set RHIZOME_ACP_CONTAINER_IT=1)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH")
	}
	image := os.Getenv("RHIZOME_ACP_FIXTURE_IMAGE")
	if image == "" {
		image = "rhizome-acp-fixture:latest"
	}
	if out, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err != nil {
		t.Fatalf("fixture image %q not built: %v\n%s", image, err, out)
	}

	cfg := &config.Config{}
	cfg.ACP.Client.Runtime = "container"
	cfg.ACP.Client.Container = &config.ACPContainerConfig{
		Engine:  "auto",
		Image:   image,
		Network: "none", // hermetic: the echo fixture needs no egress
		Pull:    "never",
	}
	cfg.Agents.List = []config.AgentConfig{{
		ID:  "container-peer",
		ACP: &config.ACPAgentConfig{}, // no Command — the image entrypoint is the agent
	}}
	reg := agent.NewAgentRegistry(cfg, nil)
	m := NewClientManager(cfg, func() *agent.AgentRegistry { return reg })
	require.NotNil(t, m)
	t.Cleanup(m.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := m.RunAgent(ctx, "container-peer", "hello-acp")
	require.NoError(t, err)
	assert.Contains(t, out, "container-echo")
	t.Logf("container runtime reply: %q", out)
}
