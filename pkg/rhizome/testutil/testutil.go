// Package testutil provides helpers shared by rhizome package tests.
//
// Test binaries running concurrently (go test's default package parallelism)
// must never share a node identity: two processes holding the same key
// authenticate to each other as the same peer, which lets unrelated tests
// cross-connect and contaminate each other's state. NewIdentity therefore
// generates a fresh random Ed25519 key per call instead of deriving one from
// a shared mnemonic.
package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

// NewIdentity returns a fresh random node identity for tests.
func NewIdentity(t *testing.T) *identity.Derived {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	lpriv, err := crypto.UnmarshalEd25519PrivateKey(priv)
	require.NoError(t, err)
	pid, err := peer.IDFromPublicKey(lpriv.GetPublic())
	require.NoError(t, err)
	return &identity.Derived{
		PrivateKey:    priv,
		PublicKey:     pub,
		Libp2pPrivKey: lpriv,
		Libp2pPubKey:  lpriv.GetPublic(),
		PeerID:        pid.String(),
	}
}
