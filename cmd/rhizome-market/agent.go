// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"encoding/json"
	"io"

	acpsdk "github.com/coder/acp-go-sdk"
)

// marketAgent is the Track 100 ACP serving skeleton: it completes the
// real protocol handshake (initialize) so buyers can verify the wire path
// end-to-end, and refuses work-bearing verbs with a track-tagged error —
// the session manager + escrow gate land in Track 102. Implementing the
// full acpsdk.Agent surface keeps the skeleton honest: a client that can
// handshake gets protocol-correct refusals, not hangs.
type marketAgent struct{}

// newAgentConn binds the agent side of an ACP connection onto a verified
// bridged conn (ndjson framing handled by the SDK — the bridge splices
// raw bytes).
func newAgentConn(agent *marketAgent, w io.Writer, r io.Reader) *acpsdk.AgentSideConnection {
	return acpsdk.NewAgentSideConnection(agent, w, r)
}

// errNotYet is the honest refusal: the verb exists on the wire, the market
// implementation lands in a later track.
func errNotYet(verb, track string) *acpsdk.RequestError {
	return &acpsdk.RequestError{
		Code:    -32601,
		Message: verb + " is not implemented in this build",
		Data:    map[string]any{"track": track},
	}
}

func (a *marketAgent) Initialize(
	_ context.Context, _ acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{},
	}, nil
}

func (a *marketAgent) Authenticate(
	_ context.Context, _ acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{},
		errNotYet("authenticate", "102")
}

func (a *marketAgent) NewSession(
	_ context.Context, _ acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	return acpsdk.NewSessionResponse{}, errNotYet("session/new", "102")
}

func (a *marketAgent) LoadSession(
	_ context.Context, _ acpsdk.LoadSessionRequest,
) (acpsdk.LoadSessionResponse, error) {
	return acpsdk.LoadSessionResponse{}, errNotYet("session/load", "102")
}

func (a *marketAgent) ListSessions(
	_ context.Context, _ acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, errNotYet("session/list", "102")
}

func (a *marketAgent) ResumeSession(
	_ context.Context, _ acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, errNotYet("session/resume", "102")
}

func (a *marketAgent) Prompt(
	_ context.Context, _ acpsdk.PromptRequest,
) (acpsdk.PromptResponse, error) {
	return acpsdk.PromptResponse{}, errNotYet("session/prompt", "102")
}

func (a *marketAgent) CloseSession(
	_ context.Context, _ acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, nil
}

func (a *marketAgent) Cancel(_ context.Context, _ acpsdk.CancelNotification) error {
	return nil
}

func (a *marketAgent) Logout(
	_ context.Context, _ acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (a *marketAgent) SetSessionMode(
	_ context.Context, _ acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, errNotYet("session/set_mode", "102")
}

func (a *marketAgent) SetSessionConfigOption(
	_ context.Context, _ acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, errNotYet("session/set_config_option", "102")
}

// HandleExtensionMethod is the `_rhizome.*` dispatch seam — the SDK routes
// extension methods here. `_rhizome.session_open` (Track 102's
// session-open handshake) already has a real dispatch point; today every
// extension method gets the same track-tagged refusal.
func (a *marketAgent) HandleExtensionMethod(
	_ context.Context, method string, _ json.RawMessage,
) (any, error) {
	return nil, errNotYet(method, "102")
}
