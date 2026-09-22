package acp

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// Client terminal policies for acp.client.terminal_policy.
const (
	TerminalPolicyDeny  = "deny"  // default: terminal/* returns method-not-found
	TerminalPolicyAllow = "allow" // serve terminal/* via the guarded exec path
)

func normalizeTerminalPolicy(p string) string {
	if strings.EqualFold(strings.TrimSpace(p), TerminalPolicyAllow) {
		return TerminalPolicyAllow
	}
	return TerminalPolicyDeny
}

// termEntry tracks one bridged terminal: owning ACP session + byte cap.
type termEntry struct {
	sessionID string
	limit     int
}

// terminalBridge serves the ACP terminal/* methods on top of the guarded
// exec-tool shell path (deny patterns + workspace-restricted cwd +
// isolation.Start). Each bridge owns a private SessionManager so terminal
// ids cannot collide with — or expose — the daemon's exec sessions.
type terminalBridge struct {
	exec *tools.ExecTool
	mgr  *tools.SessionManager

	mu    sync.Mutex
	terms map[string]termEntry
}

// newTerminalBridge builds a bridge. workspace/restrict/allowPaths mirror
// the client handler's fs sandbox posture.
func newTerminalBridge(
	workspace string,
	restrict bool,
	cfg *config.Config,
	allowPaths []*regexp.Regexp,
) (*terminalBridge, error) {
	execTool, err := tools.NewExecToolWithConfig(workspace, restrict, cfg, allowPaths)
	if err != nil {
		return nil, err
	}
	mgr := tools.NewSessionManager()
	execTool.SetSessionManager(mgr)
	return &terminalBridge{exec: execTool, mgr: mgr, terms: make(map[string]termEntry)}, nil
}

// close kills every tracked terminal and stops the manager's cleaner.
func (b *terminalBridge) close() {
	b.mu.Lock()
	ids := make([]string, 0, len(b.terms))
	for id := range b.terms {
		ids = append(ids, id)
	}
	b.terms = make(map[string]termEntry)
	b.mu.Unlock()
	for _, id := range ids {
		if sess, err := b.mgr.Get(id); err == nil {
			_ = sess.Kill()
		}
	}
	b.mgr.Stop()
}

// lookup validates a terminal id against the requesting session.
func (b *terminalBridge) lookup(id string, sid acpsdk.SessionId) (*tools.ProcessSession, error) {
	b.mu.Lock()
	entry, ok := b.terms[id]
	b.mu.Unlock()
	if !ok || entry.sessionID != string(sid) {
		return nil, fmt.Errorf("unknown terminal %q", id)
	}
	sess, err := b.mgr.Get(id)
	if err != nil {
		return nil, fmt.Errorf("unknown terminal %q", id)
	}
	return sess, nil
}

func (b *terminalBridge) create(
	_ context.Context,
	params acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	cmdline := shellQuote(params.Command, params.Args)
	cwd := ""
	if params.Cwd != nil {
		cwd = *params.Cwd
	}
	env := make([]string, 0, len(params.Env))
	for _, e := range params.Env {
		env = append(env, e.Name+"="+e.Value)
	}
	sess, err := b.exec.SpawnTerminal(cmdline, cwd, env)
	if err != nil {
		return acpsdk.CreateTerminalResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	limit := 0
	if params.OutputByteLimit != nil && *params.OutputByteLimit > 0 {
		limit = *params.OutputByteLimit
	}
	b.mu.Lock()
	b.terms[sess.ID] = termEntry{sessionID: string(params.SessionId), limit: limit}
	b.mu.Unlock()
	return acpsdk.CreateTerminalResponse{TerminalId: sess.ID}, nil
}

// tailBytes truncates s to its last n bytes on a UTF-8 boundary.
func tailBytes(s string, n int) (string, bool) {
	if n <= 0 || len(s) <= n {
		return s, false
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:], true
}

func (b *terminalBridge) output(
	_ context.Context,
	params acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	sess, err := b.lookup(params.TerminalId, params.SessionId)
	if err != nil {
		return acpsdk.TerminalOutputResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	b.mu.Lock()
	entry := b.terms[params.TerminalId]
	b.mu.Unlock()

	out := sess.PeekOutput()
	truncated := sess.OutputTruncated()
	if entry.limit > 0 {
		var cut bool
		out, cut = tailBytes(out, entry.limit)
		truncated = truncated || cut
	}
	resp := acpsdk.TerminalOutputResponse{Output: out, Truncated: truncated}
	if sess.IsDone() {
		code := sess.GetExitCode()
		resp.ExitStatus = &acpsdk.TerminalExitStatus{ExitCode: &code}
	}
	return resp, nil
}

func (b *terminalBridge) kill(
	_ context.Context,
	params acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	sess, err := b.lookup(params.TerminalId, params.SessionId)
	if err != nil {
		return acpsdk.KillTerminalResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	_ = sess.Kill() // ErrSessionDone is fine — the terminal is gone either way
	return acpsdk.KillTerminalResponse{}, nil
}

func (b *terminalBridge) release(
	_ context.Context,
	params acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	sess, err := b.lookup(params.TerminalId, params.SessionId)
	if err != nil {
		return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	_ = sess.Kill()
	b.mgr.Remove(params.TerminalId)
	b.mu.Lock()
	delete(b.terms, params.TerminalId)
	b.mu.Unlock()
	return acpsdk.ReleaseTerminalResponse{}, nil
}

func (b *terminalBridge) waitForExit(
	ctx context.Context,
	params acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	sess, err := b.lookup(params.TerminalId, params.SessionId)
	if err != nil {
		return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !sess.IsDone() {
		select {
		case <-ctx.Done():
			return acpsdk.WaitForTerminalExitResponse{}, ctx.Err()
		case <-ticker.C:
		}
	}
	code := sess.GetExitCode()
	return acpsdk.WaitForTerminalExitResponse{ExitCode: &code}, nil
}

// shellQuote joins command+args into a single shell string with
// single-quote escaping. On Windows the exec path runs through
// `powershell -Command`, which needs the `&` call operator to invoke a
// quoted command and escapes embedded quotes by doubling them; `sh -c`
// treats a bare quoted word as the command name and uses the '\” idiom.
func shellQuote(command string, args []string) string {
	q := func(s string) string {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	if runtime.GOOS == "windows" {
		q = func(s string) string {
			return "'" + strings.ReplaceAll(s, "'", "''") + "'"
		}
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, q(command))
	for _, a := range args {
		parts = append(parts, q(a))
	}
	joined := strings.Join(parts, " ")
	if runtime.GOOS == "windows" {
		return "& " + joined
	}
	return joined
}
