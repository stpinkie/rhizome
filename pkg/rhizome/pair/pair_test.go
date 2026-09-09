package pair

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// newPairNodes starts two in-process libp2p nodes and their pair managers.
func newPairNodes(t *testing.T) (*Manager, *Manager) {
	t.Helper()
	ctx := context.Background()

	idA, _, err := identity.FromMnemonic(testMnemonic, 0)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(testMnemonic, 1)
	require.NoError(t, err)

	nodeA, err := rnet.NewNode(ctx, idA.Libp2pPrivKey,
		rnet.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeA.Close() })

	nodeB, err := rnet.NewNode(ctx, idB.Libp2pPrivKey,
		rnet.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeB.Close() })

	pmA := New(nodeA.Host(), idA, t.TempDir(), Hooks{})
	pmB := New(nodeB.Host(), idB, t.TempDir(), Hooks{})
	go func() { _ = pmA.Start(ctx) }()
	go func() { _ = pmB.Start(ctx) }()
	return pmA, pmB
}

func TestPairCreateAcceptRoundTrip(t *testing.T) {
	pmA, pmB := newPairNodes(t)

	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, bundle)

	// The bundle decodes and signature-verifies.
	b, err := DecodeBundle(bundle)
	require.NoError(t, err)
	assert.Equal(t, pmA.host.ID().String(), b.PeerID)
	assert.NotEmpty(t, b.Addrs)

	var trustedByA, trustedByB []string
	pmA.hooks.TrustPeer = func(pid peer.ID) { trustedByA = append(trustedByA, pid.String()) }
	pmB.hooks.TrustPeer = func(pid peer.ID) { trustedByB = append(trustedByB, pid.String()) }
	var persistedA, persistedB []string
	pmA.hooks.Persist = func(pid string, _ []string) error { persistedA = append(persistedA, pid); return nil }
	pmB.hooks.Persist = func(pid string, _ []string) error { persistedB = append(persistedB, pid); return nil }

	peerID, err := pmB.Accept(context.Background(), bundle)
	require.NoError(t, err)
	assert.Equal(t, pmA.host.ID().String(), peerID)

	// Both sides trusted and persisted each other.
	require.Eventually(t, func() bool {
		return len(trustedByA) == 1 && len(trustedByB) == 1 &&
			len(persistedA) == 1 && len(persistedB) == 1
	}, 10*time.Second, 50*time.Millisecond)
	assert.Equal(t, pmB.host.ID().String(), trustedByA[0])
	assert.Equal(t, pmA.host.ID().String(), trustedByB[0])
}

func TestPairCodeSingleUse(t *testing.T) {
	pmA, pmB := newPairNodes(t)
	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	_, err = pmB.Accept(context.Background(), bundle)
	require.NoError(t, err)

	// Second redemption of the same code fails.
	_, err = pmB.Accept(context.Background(), bundle)
	require.Error(t, err)
}

func TestPairCodeExpired(t *testing.T) {
	pmA, pmB := newPairNodes(t)

	// Build a correctly-signed bundle whose expiry is already in the past.
	b := Bundle{
		PeerID: pmA.host.ID().String(),
		Addrs:  pmA.addrs(),
		Code:   "expired-code-01",
		Exp:    time.Now().Add(-time.Minute).Unix(),
	}
	b.Sig = identity.Sign(pmA.id.PrivateKey, invitePayload(b.PeerID, b.Code, b.Exp))
	raw, _ := json.Marshal(b)
	bundle := base64.RawURLEncoding.EncodeToString(raw)

	// The bundle decode itself rejects an expired code.
	_, err := DecodeBundle(bundle)
	require.Error(t, err)
	_, err = pmB.Accept(context.Background(), bundle)
	require.Error(t, err)
}

func TestPairBundleBadSignature(t *testing.T) {
	pmA, _ := newPairNodes(t)
	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	raw, err := base64.RawURLEncoding.DecodeString(bundle)
	require.NoError(t, err)
	var b Bundle
	require.NoError(t, json.Unmarshal(raw, &b))
	// Tamper: flip the expiry inside the signed payload.
	b.Exp += 1000
	forged, _ := json.Marshal(b)
	_, err = DecodeBundle(base64.RawURLEncoding.EncodeToString(forged))
	require.Error(t, err)
}

func TestPairUnknownCodeRejected(t *testing.T) {
	pmA, pmB := newPairNodes(t)
	// A well-formed bundle whose code pmA never issued must be rejected at
	// redemption time.
	b := Bundle{
		PeerID: pmA.host.ID().String(),
		Addrs:  pmA.addrs(),
		Code:   "deadbeefdeadbeef",
		Exp:    time.Now().Add(time.Minute).Unix(),
	}
	b.Sig = identity.Sign(pmA.id.PrivateKey, invitePayload(b.PeerID, b.Code, b.Exp))
	raw, _ := json.Marshal(b)
	bundle := base64.RawURLEncoding.EncodeToString(raw)

	_, err := pmB.Accept(context.Background(), bundle)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pairing rejected")
}
