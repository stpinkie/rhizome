package daemon

import (
	"fmt"
	"path/filepath"

	libnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"

	acpbridge "github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// startHeadlessRemoteACP serves acp.server.remote without the gateway: a
// HeadlessStack (provider + loop + bus + session index) backing a
// RemoteMux, plus the trusted-peer-gated /rhizome/acp/1.0.0 handler on the
// mesh host. Used only on the --no-gateway path — with the gateway up, the
// mux rides the gateway's agent loop instead.
func startHeadlessRemoteACP(
	cfg *config.Config,
	home string,
	m *mesh.Mesh,
) (*acpbridge.HeadlessStack, *acpbridge.RemoteMux, error) {
	var policy acpbridge.PermissionPolicy
	switch p := acpbridge.PermissionPolicy(cfg.ACP.Server.PermissionPolicy); p {
	case "", acpbridge.PermissionPrompt:
		policy = acpbridge.PermissionPrompt
	case acpbridge.PermissionAllow, acpbridge.PermissionDeny:
		policy = p
	default:
		return nil, nil, fmt.Errorf(
			"invalid acp.server.permission_policy %q (prompt|allow|deny)",
			cfg.ACP.Server.PermissionPolicy,
		)
	}

	stack, err := acpbridge.NewHeadlessStack(cfg, filepath.Join(home, "acp-sessions.json"))
	if err != nil {
		return nil, nil, err
	}
	mux := acpbridge.NewRemoteMux(stack.Loop, acpbridge.Options{
		Policy:   policy,
		Media:    stack.Media,
		Version:  config.FormatVersion(),
		Models:   stack.Models,
		Sessions: stack.Sessions,
	})
	if err := mux.Start(); err != nil {
		stack.Close()
		return nil, nil, err
	}
	stack.Bus.AddStreamDelegate(mux)

	m.Host().SetStreamHandler(protocol.ID(acpbridge.RemoteProtocolID), func(s libnet.Stream) {
		from := s.Conn().RemotePeer()
		if !m.IsTrusted(from) {
			logger.WarnCF("acp", "remote: refused untrusted peer",
				map[string]any{"peer": from.String()})
			_ = s.Reset()
			return
		}
		mux.Serve(s)
	})
	logger.InfoCF("acp", "remote ACP serving (headless) on "+acpbridge.RemoteProtocolID,
		map[string]any{"policy": string(policy)})
	return stack, mux, nil
}
