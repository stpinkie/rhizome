// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/config"
)

// acpMarketProtocol is the bridged stream protocol the market claims.
const acpMarketProtocol = "/rhizome/acp/1.0.0"

// buyerInitTimeout bounds the ACP initialize handshake on a fresh conn.
const buyerInitTimeout = 30 * time.Second

// buyerClientHandler is the ACP client the buy path runs as — zero real
// capabilities (the seller's agent must not reach into the buyer's
// filesystem/terminal): session updates accumulate into the result text
// the receipt's result_sha256 is verified against; every other client
// verb is denied.
type buyerClientHandler struct {
	mu     sync.Mutex
	result strings.Builder
}

var _ acpsdk.Client = (*buyerClientHandler)(nil)

func (h *buyerClientHandler) SessionUpdate(
	_ context.Context, params acpsdk.SessionNotification,
) error {
	if text := agentTextDelta(params.Update); text != "" {
		h.mu.Lock()
		h.result.WriteString(text)
		h.mu.Unlock()
	}
	return nil
}

func (h *buyerClientHandler) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result.String()
}

func (h *buyerClientHandler) RequestPermission(
	_ context.Context, _ acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
		},
	}, nil
}

func denyCap(verb string) error {
	return &acpsdk.RequestError{
		Code:    -32601,
		Message: "market buyers expose no capabilities (fs/terminal denied)",
		Data:    map[string]any{"verb": verb},
	}
}

func (h *buyerClientHandler) ReadTextFile(
	context.Context, acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, denyCap("fs/read_text_file")
}

func (h *buyerClientHandler) WriteTextFile(
	context.Context, acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, denyCap("fs/write_text_file")
}

func (h *buyerClientHandler) CreateTerminal(
	context.Context, acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, denyCap("terminal/create")
}

func (h *buyerClientHandler) KillTerminal(
	context.Context, acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, denyCap("terminal/kill")
}

func (h *buyerClientHandler) TerminalOutput(
	context.Context, acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, denyCap("terminal/output")
}

func (h *buyerClientHandler) ReleaseTerminal(
	context.Context, acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, denyCap("terminal/release")
}

func (h *buyerClientHandler) WaitForTerminalExit(
	context.Context, acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, denyCap("terminal/wait_for_exit")
}

// buyerSessionResult is what a purchase carries back from the wire: the
// accumulated result text and whatever the seller reported.
type buyerSessionResult struct {
	ResultText string
	StopReason string
}

// runBuyerSession drives one purchase's ACP session over the bridged
// conn: initialize → _rhizome.session_open → session/new → session/prompt
// (single prompt — the seller refuses a second). The result text is what
// the receipt's result_sha256 must hash to.
func runBuyerSession(
	ctx context.Context,
	conn io.ReadWriteCloser,
	p *purchase,
) (*buyerSessionResult, error) {
	handler := &buyerClientHandler{}
	cli := acpsdk.NewClientSideConnection(handler, conn, conn)
	initCtx, cancel := context.WithTimeout(ctx, buyerInitTimeout)
	defer cancel()
	if _, err := cli.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion:    acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{},
		ClientInfo: &acpsdk.Implementation{
			Name: "rhizome-market", Version: config.FormatVersion(),
		},
	}); err != nil {
		return nil, fmt.Errorf("acp initialize: %w", err)
	}

	// The session_open presentation commits the escrow to this task.
	openCtx, openCancel := context.WithTimeout(ctx, 30*time.Second)
	_, err := cli.CallExtension(openCtx, "_rhizome.session_open", map[string]any{
		"session_id": p.SessionID,
		"task_hash":  p.TaskHash,
		"offer_id":   p.OfferID,
		"buyer":      p.Buyer,
		"terms": map[string]any{
			"amount":   p.Terms.Amount,
			"token":    p.Terms.Token,
			"chain_id": p.Terms.ChainID,
		},
	})
	openCancel()
	if err != nil {
		return nil, fmt.Errorf("session_open refused: %w", err)
	}

	newCtx, newCancel := context.WithTimeout(ctx, 60*time.Second)
	sess, err := cli.NewSession(newCtx, acpsdk.NewSessionRequest{
		Cwd: "/", McpServers: []acpsdk.McpServer{},
	})
	newCancel()
	if err != nil {
		return nil, fmt.Errorf("session/new: %w", err)
	}

	// One text block carrying the exact reviewed/redacted task (its bytes
	// are what the seller hashes against task_hash), then any approved
	// attachments as URI references — never embedded contents.
	blocks := []acpsdk.ContentBlock{acpsdk.TextBlock(p.Task)}
	for _, uri := range p.Attachments {
		blocks = append(blocks, acpsdk.ContentBlock{
			ResourceLink: &acpsdk.ContentBlockResourceLink{
				Type: "resource_link", Uri: uri, Name: uri,
			},
		})
	}
	resp, err := cli.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    blocks,
	})
	if err != nil {
		return nil, fmt.Errorf("session/prompt: %w", err)
	}
	return &buyerSessionResult{
		ResultText: handler.text(),
		StopReason: string(resp.StopReason),
	}, nil
}

// fetchReceipt asks the seller for the session's signed receipt over a
// fresh conn — called after the prompt completes.
func fetchReceipt(
	ctx context.Context, conn io.ReadWriteCloser, sessionID string,
) (*receipt, error) {
	handler := &buyerClientHandler{}
	cli := acpsdk.NewClientSideConnection(handler, conn, conn)
	initCtx, cancel := context.WithTimeout(ctx, buyerInitTimeout)
	defer cancel()
	if _, err := cli.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name: "rhizome-market", Version: config.FormatVersion(),
		},
	}); err != nil {
		return nil, fmt.Errorf("acp initialize: %w", err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 30*time.Second)
	defer rcancel()
	raw, err := cli.CallExtension(rctx, "_rhizome.receipt", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var rc receipt
	if err := json.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("receipt decode: %w", err)
	}
	return &rc, nil
}
