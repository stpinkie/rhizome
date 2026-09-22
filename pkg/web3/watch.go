// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/fileutil"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// Log watches: daemon-side eth_getLogs pollers that turn contract events
// into web3.event runtime events. The nimbus verified proxy exposes no
// subscription RPC, so polling is the only option — lookback is capped by
// tools.web3.max_log_range and watch state persists across restarts.

const (
	watchMaxCount      = 8
	watchStateFile     = "watches.json"
	watchMaxPerTick    = 50  // bound events per watch per tick
	watchFirstLookback = 500 // blocks scanned on a watch's first run
)

// watchState persists each watch's last-scanned block.
type watchState struct {
	LastBlock map[string]uint64 `json:"last_block"`
}

// WatchRunner polls configured watches on their intervals.
type WatchRunner struct {
	provider  *Provider
	registry  *ABIRegistry
	watches   []config.Web3WatchConfig
	stateDir  string // <RHIZOME_HOME>/web3
	maxRange  uint64
	confDepth uint64
	emit      func(kind string, attrs map[string]any)

	mu       sync.Mutex
	last     map[string]uint64
	resolved map[string]string // watch name → contract address
}

// NewWatchRunner builds the poller. emit may be nil (events dropped).
// watches are validated/bounded; entries that fail validation are skipped
// with a log line rather than failing startup.
func NewWatchRunner(
	provider *Provider, registry *ABIRegistry, watches []config.Web3WatchConfig,
	stateDir string, maxRange, confDepth uint64, emit func(string, map[string]any),
) *WatchRunner {
	if maxRange == 0 {
		maxRange = 10000
	}
	if confDepth > 64 {
		confDepth = 64
	}
	w := &WatchRunner{
		provider: provider, registry: registry, stateDir: stateDir,
		maxRange: maxRange, confDepth: confDepth, emit: emit,
		last: map[string]uint64{}, resolved: map[string]string{},
	}
	for i, wc := range watches {
		if i >= watchMaxCount {
			logger.WarnCF("web3", "watch cap reached — dropping the rest",
				map[string]any{"max": watchMaxCount})
			break
		}
		if wc.Name == "" || wc.Contract == "" {
			logger.WarnCF("web3", "watch needs name and contract — skipped",
				map[string]any{"index": i})
			continue
		}
		badTopic := false
		for _, tp := range wc.Topics {
			if tp != "" && !IsHash32(tp) {
				logger.WarnCF("web3", "watch topic invalid — watch skipped",
					map[string]any{"watch": wc.Name, "topic": tp})
				badTopic = true
			}
		}
		if badTopic {
			continue
		}
		w.watches = append(w.watches, wc)
	}
	return w
}

// Len reports how many watches are active.
func (r *WatchRunner) Len() int { return len(r.watches) }

// StatePath exposes the watch-state file for tests/docs.
func (r *WatchRunner) StatePath() string {
	return filepath.Join(r.stateDir, watchStateFile)
}

// loadState reads persisted last-blocks (best effort — a corrupt file
// starts watches fresh rather than crashing the daemon).
func (r *WatchRunner) loadState() {
	data, err := os.ReadFile(r.StatePath())
	if err != nil {
		return
	}
	var s watchState
	if json.Unmarshal(data, &s) == nil && s.LastBlock != nil {
		r.last = s.LastBlock
	}
}

func (r *WatchRunner) saveState() {
	data, err := json.Marshal(watchState{LastBlock: r.last})
	if err != nil {
		return
	}
	if err := os.MkdirAll(r.stateDir, walletDirPerms); err != nil {
		return
	}
	if err := fileutil.WriteFileAtomic(r.StatePath(), data, walletFilePerms); err != nil {
		logger.WarnCF("web3", "watch state save failed", map[string]any{"error": err.Error()})
	}
}

// Run polls until ctx is cancelled. Blocks the caller — run in a goroutine.
func (r *WatchRunner) Run(ctx context.Context) {
	if len(r.watches) == 0 || r.provider == nil {
		return
	}
	r.mu.Lock()
	r.loadState()
	r.mu.Unlock()

	// Resolve contract refs once at start — labels/ENS are stable config.
	for _, w := range r.watches {
		rc, err := ResolveContract(ctx, r.provider, r.registry, w.Contract)
		if err != nil {
			logger.ErrorCF("web3", "watch contract did not resolve — disabled",
				map[string]any{"watch": w.Name, "contract": w.Contract, "error": err.Error()})
			continue
		}
		r.mu.Lock()
		r.resolved[w.Name] = rc.Address
		r.mu.Unlock()
	}

	type due struct {
		w    config.Web3WatchConfig
		next time.Time
	}
	queue := make([]due, 0, len(r.watches))
	for _, w := range r.watches {
		if r.resolved[w.Name] != "" {
			queue = append(queue, due{w: w, next: time.Now()})
		}
	}
	if len(queue) == 0 {
		return
	}
	scan := func() {
		now := time.Now()
		for i := range queue {
			if now.Before(queue[i].next) {
				continue
			}
			queue[i].next = now.Add(queue[i].w.GetInterval())
			r.pollWatch(ctx, &queue[i].w)
		}
	}
	scan() // immediate pass — catch up on state after a daemon restart
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.saveState()
			r.mu.Unlock()
			return
		case <-tick.C:
			scan()
		}
	}
}

// pollWatch fetches and emits new logs for one watch.
func (r *WatchRunner) pollWatch(ctx context.Context, w *config.Web3WatchConfig) {
	r.mu.Lock()
	addr := r.resolved[w.Name]
	from := r.last[w.Name]
	r.mu.Unlock()
	if addr == "" {
		return
	}
	rawHead, err := r.provider.Call(ctx, "eth_blockNumber", nil)
	if err != nil {
		logger.WarnCF("web3", "watch head lookup failed",
			map[string]any{"watch": w.Name, "error": err.Error()})
		return
	}
	var headHex string
	if err := json.Unmarshal(rawHead, &headHex); err != nil {
		return
	}
	head, err := QuantityUint64(headHex)
	if err != nil {
		return
	}
	// Emit only logs with confDepth confirmations — reorg protection.
	if head <= r.confDepth {
		return
	}
	head -= r.confDepth
	if from == 0 {
		// First run: scan a bounded lookback rather than all history.
		if head > watchFirstLookback {
			from = head - watchFirstLookback
		} else {
			from = 1
		}
	} else {
		from++
	}
	if from > head {
		return
	}
	to := head
	if to-from+1 > r.maxRange {
		to = from + r.maxRange - 1 // bound the range; next tick continues
	}
	filter := map[string]any{
		"address":   addr,
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
	}
	if len(w.Topics) > 0 {
		topics := make([]any, 0, len(w.Topics))
		for _, tp := range w.Topics {
			if tp == "" {
				topics = append(topics, nil)
			} else {
				topics = append(topics, tp)
			}
		}
		filter["topics"] = topics
	}
	raw, err := r.provider.Call(ctx, "eth_getLogs", []any{filter})
	if err != nil {
		logger.WarnCF("web3", "watch poll failed",
			map[string]any{"watch": w.Name, "error": err.Error()})
		return
	}
	logs, _, err := DecodeLogs(raw, watchMaxPerTick)
	if err != nil {
		logger.WarnCF("web3", "watch log decode failed",
			map[string]any{"watch": w.Name, "error": err.Error()})
		return
	}
	for _, lg := range logs {
		if r.emit != nil {
			r.emit("web3.event", map[string]any{
				"watch":   w.Name,
				"address": lg.Address,
				"block":   lg.BlockNumber,
				"tx":      lg.TxHash,
				"index":   lg.LogIndex,
				"topics":  lg.Topics,
				"data":    lg.Data,
			})
		}
	}
	r.mu.Lock()
	r.last[w.Name] = to
	r.mu.Unlock()
}
