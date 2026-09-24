// Package p2putil provides small libp2p helpers used by the Rhizome mesh,
// agent RPC, task, and sync transports.
package p2putil

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// OpenProtocolStream opens a libp2p stream for the given protocol. If the peer
// rejects the protocol as not supported (for example because its stream handler
// has not finished registering during startup), it retries until the timeout
// expires. This avoids the identify/peerstore race where the local peerstore is
// not yet updated with the remote's protocol set.
func OpenProtocolStream(
	ctx context.Context,
	h host.Host,
	pid peer.ID,
	proto protocol.ID,
	timeout time.Duration,
) (network.Stream, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		s, err := h.NewStream(ctx, pid, proto)
		if err == nil {
			return s, nil
		}
		if !isProtocolNotSupported(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			if err := ctx.Err(); err == context.DeadlineExceeded {
				return nil, fmt.Errorf("peer %s does not support %s", pid, proto)
			}
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func isProtocolNotSupported(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "protocols not supported") ||
		strings.Contains(msg, "protocol not supported") ||
		strings.Contains(msg, "failed to negotiate")
}

// ResolvePeerAddr accepts a bare peer ID or a full peer multiaddr. A
// multiaddr's addresses are added to the host's peerstore so dialing works
// for freshly discovered peers (module stream bridge, acp.remote).
func ResolvePeerAddr(h host.Host, s string) (peer.ID, error) {
	if pid, err := peer.Decode(s); err == nil {
		return pid, nil
	}
	ai, err := peer.AddrInfoFromString(s)
	if err != nil {
		return "", fmt.Errorf("unparseable peer %q", s)
	}
	h.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.TempAddrTTL)
	return ai.ID, nil
}
