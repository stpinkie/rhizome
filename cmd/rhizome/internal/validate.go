package internal

import (
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	rswarm "github.com/stpinkie/rhizome/pkg/rhizome/swarm"
	"github.com/stpinkie/rhizome/pkg/routing"
)

// ValidatePeerID checks that s is a valid libp2p peer ID.
func ValidatePeerID(s string) error {
	if _, err := peer.Decode(s); err != nil {
		return fmt.Errorf("invalid peer id %q: %w", s, err)
	}
	return nil
}

// ValidateMultiaddr checks that s is a valid multiaddr.
func ValidateMultiaddr(s string) error {
	if _, err := multiaddr.NewMultiaddr(s); err != nil {
		return fmt.Errorf("invalid multiaddr %q: %w", s, err)
	}
	return nil
}

// ValidateMultiaddrWithPeerID checks that s is a valid multiaddr containing a
// /p2p/<peer-id> component.
func ValidateMultiaddrWithPeerID(s string) error {
	maddr, err := multiaddr.NewMultiaddr(s)
	if err != nil {
		return fmt.Errorf("invalid multiaddr %q: %w", s, err)
	}
	if _, err := peer.AddrInfoFromP2pAddr(maddr); err != nil {
		return fmt.Errorf("multiaddr %q does not contain a valid peer id: %w", s, err)
	}
	return nil
}

// ValidateAgentID checks that s is a valid agent ID and returns the normalized
// (lower-case) form.
func ValidateAgentID(s string) (string, error) {
	normalized := routing.NormalizeAgentID(s)
	if strings.ToLower(strings.TrimSpace(s)) != normalized {
		return "", fmt.Errorf("invalid agent id %q (must match [a-zA-Z0-9][a-zA-Z0-9_-]{0,63})", s)
	}
	return normalized, nil
}

// ValidateSwarmID checks that s is a valid swarm ID.
func ValidateSwarmID(s string) error {
	if !rswarm.ValidSwarmID(s) {
		return fmt.Errorf("invalid swarm id %q (allowed: [a-zA-Z0-9_.-], 1-64 chars)", s)
	}
	return nil
}

// ValidateTaskID checks that s is a non-empty task ID of reasonable length.
func ValidateTaskID(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("task id is empty")
	}
	if len(s) > 128 {
		return fmt.Errorf("task id %q is too long (>128 chars)", s)
	}
	return nil
}

// ValidateScatterStrategy checks that s is a supported fan-out strategy.
func ValidateScatterStrategy(s string) (string, error) {
	switch strings.ToLower(s) {
	case "first", "quorum", "all":
		return strings.ToLower(s), nil
	default:
		return "", fmt.Errorf("invalid scatter strategy %q (allowed: first, quorum, all)", s)
	}
}
