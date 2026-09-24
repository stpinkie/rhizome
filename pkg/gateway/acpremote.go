package gateway

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	libnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
)

const remoteACPDialTimeout = 15 * time.Second

// remoteACPDialer builds the trust-gated acp.RemoteDialer for
// agents.list[].acp.remote bindings: the configured peer must be in the
// mesh trust set before any stream is opened. A full multiaddr's addresses
// are added to the peerstore only after the trust check passes.
func remoteACPDialer(m *mesh.Mesh) acp.RemoteDialer {
	return func(ctx context.Context, agentID, remote string) (io.ReadWriteCloser, error) {
		var pid peer.ID
		if decoded, err := peer.Decode(remote); err == nil {
			pid = decoded
		} else {
			ai, err := peer.AddrInfoFromString(remote)
			if err != nil {
				return nil, fmt.Errorf("acp.remote: unparseable peer %q", remote)
			}
			pid = ai.ID
			if !m.IsTrusted(pid) {
				return nil, fmt.Errorf("acp.remote: peer %s is not trusted", pid)
			}
			m.Host().Peerstore().AddAddrs(pid, ai.Addrs, peerstore.TempAddrTTL)
		}
		if !m.IsTrusted(pid) {
			return nil, fmt.Errorf("acp.remote: peer %s is not trusted", pid)
		}
		return p2putil.OpenProtocolStream(
			ctx, m.Host(), pid, protocol.ID(acp.RemoteProtocolID), remoteACPDialTimeout,
		)
	}
}

// startRemoteACP serves acp.server.remote: a RemoteMux multiplexed over the
// gateway's agent loop + message bus, plus the trusted-peer-gated
// /rhizome/acp/1.0.0 stream handler on the mesh host.
func startRemoteACP(
	cfg *config.Config,
	homePath string,
	agentLoop acp.AgentRunner,
	msgBus *bus.MessageBus,
	mediaStore media.MediaStore,
	m *mesh.Mesh,
) (*acp.RemoteMux, error) {
	policy, err := remoteACPPolicy(cfg)
	if err != nil {
		return nil, err
	}
	var sessions *acp.SessionStore
	if store, err := acp.OpenSessionStore(filepath.Join(homePath, "acp-sessions.json")); err != nil {
		logger.WarnCF("acp", "remote: session store unavailable — session/load disabled",
			map[string]any{"error": err.Error()})
	} else {
		sessions = store
	}
	mux := acp.NewRemoteMux(agentLoop, acp.Options{
		Policy:   policy,
		Media:    mediaStore,
		Version:  config.FormatVersion(),
		Models:   acp.SelectableModels(cfg),
		Sessions: sessions,
	})
	if err := mux.Start(); err != nil {
		return nil, err
	}
	msgBus.AddStreamDelegate(mux)
	registerRemoteACPHandler(m, mux)
	logger.InfoCF("acp", "remote ACP serving on "+acp.RemoteProtocolID,
		map[string]any{"policy": string(policy)})
	return mux, nil
}

// registerRemoteACPHandler installs the /rhizome/acp/1.0.0 stream handler:
// inbound connections are accepted only from trusted mesh peers; anything
// else is reset.
func registerRemoteACPHandler(m *mesh.Mesh, mux *acp.RemoteMux) {
	m.Host().SetStreamHandler(protocol.ID(acp.RemoteProtocolID), func(s libnet.Stream) {
		from := s.Conn().RemotePeer()
		if !m.IsTrusted(from) {
			logger.WarnCF("acp", "remote: refused untrusted peer",
				map[string]any{"peer": from.String()})
			_ = s.Reset()
			return
		}
		mux.Serve(s)
	})
}

func remoteACPPolicy(cfg *config.Config) (acp.PermissionPolicy, error) {
	switch policy := acp.PermissionPolicy(cfg.ACP.Server.PermissionPolicy); policy {
	case "", acp.PermissionPrompt:
		return acp.PermissionPrompt, nil
	case acp.PermissionAllow, acp.PermissionDeny:
		return policy, nil
	default:
		return "", fmt.Errorf(
			"invalid acp.server.permission_policy %q (prompt|allow|deny)",
			cfg.ACP.Server.PermissionPolicy,
		)
	}
}
