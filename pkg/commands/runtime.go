package commands

import (
	"context"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

type MCPServerInfo struct {
	Name      string
	Enabled   bool
	Deferred  bool
	Connected bool
	ToolCount int
}

type MCPToolParameterInfo struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

type MCPToolInfo struct {
	Name        string
	Description string
	Parameters  []MCPToolParameterInfo
}

// ContextStats describes current session context window usage.
type ContextStats struct {
	UsedTokens        int
	TotalTokens       int // model context window
	HistoryTokens     int // history-only tokens (what maybeSummarize checks)
	CompressAtTokens  int // hard budget compression threshold
	SummarizeAtTokens int // soft summarization trigger
	UsedPercent       int // 0-100
	MessageCount      int
}

// StopResult describes the outcome of a stop request for the current session.
type StopResult struct {
	Stopped  bool
	TaskName string
}

// Runtime provides runtime dependencies to command handlers. It is constructed
// per-request by the agent loop so that per-request state (like session scope)
// can coexist with long-lived callbacks (like GetModelInfo).
type Runtime struct {
	Config             *config.Config
	GetModelInfo       func() (name, provider string)
	AskSideQuestion    func(ctx context.Context, question string) (string, error)
	ListAgentIDs       func() []string
	ListDefinitions    func() []Definition
	ListSkillNames     func() []string
	ListMCPServers     func(ctx context.Context) []MCPServerInfo
	ListMCPTools       func(ctx context.Context, serverName string) ([]MCPToolInfo, error)
	GetEnabledChannels func() []string
	GetActiveTurn      func() any // Returning any to avoid circular dependency with agent package
	GetContextStats    func() *ContextStats
	SwitchModel        func(value string) (oldModel string, err error)
	SwitchChannel      func(value string) error
	ClearHistory       func() error
	ReloadConfig       func() error
	StopActiveTurn     func() (StopResult, error)
	// Web3 approval queue (scope-gated by tools.web3.approval_channels).
	ListWeb3Pending func() ([]Web3PendingInfo, error)
	// ResolveWeb3 resolves a pending id; `by` records the resolver
	// ("channel:<chan>:<chat>"). approve executes sign+broadcast.
	ResolveWeb3 func(ctx context.Context, id string, approve bool, by string) (*Web3ResolveResult, error)
}

// Web3PendingInfo is the channel-command view of a queued signing request.
type Web3PendingInfo struct {
	ID        string
	Kind      string
	Summary   string
	From      string
	To        string
	ChainID   uint64
	ExpiresAt time.Time
}

// Web3ResolveResult is the resolved entry's outcome.
type Web3ResolveResult struct {
	ID     string
	Status string // pending|sent|done|rejected|failed|expired
	TxHash string
	Result string
	Error  string
}
