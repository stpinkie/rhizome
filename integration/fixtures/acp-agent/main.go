// Minimal Agent Client Protocol agent for the acp-container integration
// suite: serves initialize + session/new + prompt over stdin/stdout with a
// canned echo, proving the ACP wire protocol survives a real
// `docker run -i --rm` transport (v0.14.0 Track 97).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

type echoAgent struct {
	conn *acpsdk.AgentSideConnection
	n    int
}

func (a *echoAgent) Initialize(
	_ context.Context,
	req acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{},
	}, nil
}

func (a *echoAgent) NewSession(
	_ context.Context,
	_ acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	a.n++
	return acpsdk.NewSessionResponse{
		SessionId: acpsdk.SessionId(fmt.Sprintf("fixture-sess-%d", a.n)),
	}, nil
}

func (a *echoAgent) Prompt(
	ctx context.Context,
	req acpsdk.PromptRequest,
) (acpsdk.PromptResponse, error) {
	var text strings.Builder
	for _, block := range req.Prompt {
		if block.Text != nil {
			text.WriteString(block.Text.Text)
		}
	}
	if a.conn != nil {
		_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: req.SessionId,
			Update:    acpsdk.UpdateAgentMessageText("container-echo: " + text.String()),
		})
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func (a *echoAgent) Cancel(_ context.Context, _ acpsdk.CancelNotification) error { return nil }

func (a *echoAgent) CloseSession(
	_ context.Context,
	_ acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, nil
}

func (a *echoAgent) LoadSession(
	_ context.Context,
	_ acpsdk.LoadSessionRequest,
) (acpsdk.LoadSessionResponse, error) {
	return acpsdk.LoadSessionResponse{}, nil
}

func (a *echoAgent) ListSessions(
	_ context.Context,
	_ acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, nil
}

func (a *echoAgent) ResumeSession(
	_ context.Context,
	_ acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *echoAgent) Logout(
	_ context.Context,
	_ acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (a *echoAgent) Authenticate(
	_ context.Context,
	_ acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, nil
}

func (a *echoAgent) SetSessionMode(
	_ context.Context,
	_ acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, nil
}

func (a *echoAgent) SetSessionConfigOption(
	_ context.Context,
	_ acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}

func (a *echoAgent) main(ctx context.Context) {
	a.conn = acpsdk.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	<-ctx.Done()
}

func main() {
	log.SetOutput(os.Stderr) // stdout is the ACP transport — keep it clean
	log.Print("acp fixture agent ready")
	a := &echoAgent{}
	a.main(context.Background())
}
