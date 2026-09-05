package mesh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

const (
	// maxStoredTasks bounds the task store so a peer cannot exhaust memory by
	// submitting unbounded tasks.
	maxStoredTasks = 256
	// taskResultTTL is how long a completed task's result is kept for
	// retrieval before it may be evicted.
	taskResultTTL = time.Hour
)

// MeshTask is a single remote task owned by the callee.
type MeshTask struct {
	ID         string
	CorrID     string
	Owner      peer.ID
	AgentID    string
	Model      string
	Status     agenttask.TaskStatus
	Result     *toolshared.ToolResult
	Err        string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	cancelFunc context.CancelFunc
	done       chan struct{}
}

// TaskStore tracks remote tasks submitted to this node. Tasks are keyed by
// task id and owned by the submitting peer; only the owner may query, fetch
// results for, or cancel a task.
type TaskStore struct {
	mu     sync.Mutex
	tasks  map[string]*MeshTask
	byCorr map[peer.ID]map[string]string // owner -> correlation id -> task id
	closed bool
}

// NewTaskStore creates an empty task store.
func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks:  make(map[string]*MeshTask),
		byCorr: make(map[peer.ID]map[string]string),
	}
}

func newTaskID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("task-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// Submit registers a new task. If the same peer resubmits the same
// correlation id, the existing task is returned with created=false.
func (s *TaskStore) Submit(owner peer.ID, req agenttask.Request) (task *MeshTask, created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, false, fmt.Errorf("task store is closed")
	}

	if req.CorrelationID != "" {
		if byID, ok := s.byCorr[owner]; ok {
			if id, ok := byID[req.CorrelationID]; ok {
				if existing, ok := s.tasks[id]; ok {
					return existing, false, nil
				}
			}
		}
	}

	s.evictLocked()

	task = &MeshTask{
		ID:        newTaskID(),
		CorrID:    req.CorrelationID,
		Owner:     owner,
		AgentID:   req.TargetAgentID,
		Model:     req.Model,
		Status:    agenttask.StatusAccepted,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		done:      make(chan struct{}),
	}
	s.tasks[task.ID] = task
	if req.CorrelationID != "" {
		if _, ok := s.byCorr[owner]; !ok {
			s.byCorr[owner] = make(map[string]string)
		}
		s.byCorr[owner][req.CorrelationID] = task.ID
	}
	return task, true, nil
}

// evictLocked drops terminal tasks past the TTL and, when the store is full,
// the oldest terminal tasks first. It must be called with s.mu held.
func (s *TaskStore) evictLocked() {
	now := time.Now()
	for id, t := range s.tasks {
		if t.Status.Terminal() && now.Sub(t.UpdatedAt) > taskResultTTL {
			s.deleteLocked(id)
		}
	}
	for len(s.tasks) >= maxStoredTasks {
		// Prefer evicting the oldest terminal task; fall back to the oldest
		// task overall so the store never grows without bound.
		var oldest *MeshTask
		var oldestTerminal *MeshTask
		for _, t := range s.tasks {
			if oldest == nil || t.CreatedAt.Before(oldest.CreatedAt) {
				oldest = t
			}
			if t.Status.Terminal() &&
				(oldestTerminal == nil || t.UpdatedAt.Before(oldestTerminal.UpdatedAt)) {
				oldestTerminal = t
			}
		}
		victim := oldestTerminal
		if victim == nil {
			victim = oldest
		}
		if victim == nil {
			return
		}
		if !victim.Status.Terminal() && victim.cancelFunc != nil {
			victim.cancelFunc()
		}
		s.deleteLocked(victim.ID)
	}
}

func (s *TaskStore) deleteLocked(id string) {
	t, ok := s.tasks[id]
	if !ok {
		return
	}
	delete(s.tasks, id)
	if byID, ok := s.byCorr[t.Owner]; ok {
		if t.CorrID != "" && byID[t.CorrID] == id {
			delete(byID, t.CorrID)
		}
		if len(byID) == 0 {
			delete(s.byCorr, t.Owner)
		}
	}
}

// getOwned returns the task only if it belongs to the given peer.
func (s *TaskStore) getOwned(id string, owner peer.ID) (*MeshTask, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Owner != owner {
		return nil, false
	}
	return t, true
}

// Start marks an accepted task as running and registers its cancel func.
func (s *TaskStore) Start(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok && t.Status == agenttask.StatusAccepted {
		t.Status = agenttask.StatusRunning
		t.UpdatedAt = time.Now().UTC()
		t.cancelFunc = cancel
	}
}

// Finish records the terminal status and result of a task.
func (s *TaskStore) Finish(id string, status agenttask.TaskStatus, result *toolshared.ToolResult, taskErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok && !t.Status.Terminal() {
		t.Status = status
		t.Result = result
		t.Err = taskErr
		t.UpdatedAt = time.Now().UTC()
		close(t.done)
	}
}

// Cancel marks a task cancelled and invokes its cancel func. Only the owner
// may cancel. Returns false if the task does not exist or is not owned.
func (s *TaskStore) Cancel(id string, owner peer.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Owner != owner {
		return false
	}
	if !t.Status.Terminal() {
		t.Status = agenttask.StatusCancelled
		t.Err = "cancelled by owner"
		t.UpdatedAt = time.Now().UTC()
		if t.cancelFunc != nil {
			t.cancelFunc()
		}
		select {
		case <-t.done:
		default:
			close(t.done)
		}
	}
	return true
}

// Wait blocks until the task reaches a terminal state, the wait duration
// elapses, or ctx is canceled. Returns the task (possibly still running).
func (s *TaskStore) Wait(ctx context.Context, id string, owner peer.ID, wait time.Duration) (*MeshTask, bool) {
	t, ok := s.getOwned(id, owner)
	if !ok {
		return nil, false
	}
	if t.Status.Terminal() || wait <= 0 {
		return t, true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-t.done:
	}
	return t, true
}

// List returns info for every task owned by the given peer.
func (s *TaskStore) List(owner peer.ID) []agenttask.TaskInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agenttask.TaskInfo, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.Owner != owner {
			continue
		}
		out = append(out, t.Info())
	}
	return out
}

// Info snapshots the public view of a task.
func (t *MeshTask) Info() agenttask.TaskInfo {
	return agenttask.TaskInfo{
		TaskID:    t.ID,
		Status:    t.Status,
		AgentID:   t.AgentID,
		Model:     t.Model,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
		Error:     t.Err,
	}
}

// Close cancels every non-terminal task and prevents new submissions.
func (s *TaskStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, t := range s.tasks {
		if t.Status.Terminal() {
			continue
		}
		t.Status = agenttask.StatusCancelled
		t.Err = "mesh stopped"
		t.UpdatedAt = time.Now().UTC()
		if t.cancelFunc != nil {
			t.cancelFunc()
		}
		select {
		case <-t.done:
		default:
			close(t.done)
		}
	}
}
