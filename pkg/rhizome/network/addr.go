package network

import (
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// PeerIDFromMultiaddr extracts the /p2p/<peer-id> component from a multiaddr.
// It trims whitespace before parsing and returns an error if the address is
// malformed or does not contain a peer id.
func PeerIDFromMultiaddr(addr string) (peer.ID, error) {
	maddr, err := multiaddr.NewMultiaddr(strings.TrimSpace(addr))
	if err != nil {
		return "", err
	}
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// AppendUnique appends value to items only if it is not already present.
// The comparison is exact; callers should trim whitespace before calling if
// normalization is required.
func AppendUnique(items []string, value string) []string {
	for _, existing := range items {
		if existing == value {
			return items
		}
	}
	return append(items, value)
}
