// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stpinkie/rhizome/pkg/isolation"
	"github.com/stpinkie/rhizome/pkg/logger"
)

const (
	// maxLogBytes caps each stdout/stderr log; on (re)start a log over the
	// cap is rotated to <name>.old, keeping at most two generations.
	maxLogBytes = 4 << 20

	restartMinDelay = 1 * time.Second
	restartMaxDelay = 30 * time.Second
	// restartResetAfter: a process that stayed up this long resets the
	// backoff counter.
	restartResetAfter = 60 * time.Second
)

// proc is one supervised process instance. Each instance has its own
// monitor goroutine; done closes when that monitor exits.
type proc struct {
	cmd      *exec.Cmd
	stopping atomic.Bool // set by Stop — suppresses auto-restart
	done     chan struct{}
}

// Supervisor owns running module processes for the daemon. It launches
// daemon-kind modules through pkg/isolation, restarts them on unexpected
// exit with exponential backoff, and publishes module.* lifecycle events.
// Processes are tied to the supervisor's own context — never to a
// request-scoped context — so an HTTP request ending cannot kill a module.
type Supervisor struct {
	mgr     *Manager
	ctx     context.Context
	cancel  context.CancelFunc
	stopCh  chan struct{} // closed by StopAll to interrupt backoff sleeps
	mu      sync.Mutex
	running map[string]*proc
	wg      sync.WaitGroup
	closed  atomic.Bool
}

// NewSupervisor builds a supervisor bound to a manager.
func NewSupervisor(mgr *Manager) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{
		mgr:     mgr,
		ctx:     ctx,
		cancel:  cancel,
		stopCh:  make(chan struct{}),
		running: map[string]*proc{},
	}
	mgr.SetSupervisor(s)
	return s
}

// IsRunning reports whether the module currently has a live process.
func (s *Supervisor) IsRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

// StartEnabled launches every enabled daemon-kind module. Errors are
// logged and do not prevent the remaining modules from starting.
func (s *Supervisor) StartEnabled() {
	for _, spec := range s.mgr.specs() {
		if spec.Kind != KindDaemon {
			continue
		}
		if !s.mgr.moduleConfig(spec.ID).Enabled {
			continue
		}
		if err := s.Start(spec.ID); err != nil {
			logger.WarnCF("modules", "module autostart failed",
				map[string]any{"module": spec.ID, "error": err.Error()})
		}
	}
}

// Start launches a daemon-kind module and begins supervising it.
func (s *Supervisor) Start(id string) error {
	if s.closed.Load() {
		return fmt.Errorf("module supervisor is shut down")
	}
	spec, _, ok := s.mgr.lookupSpec(id)
	if !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	if spec.Kind == KindConfig {
		return fmt.Errorf("module %q is config-only — nothing to run", id)
	}
	if spec.Kind == KindOnDemand {
		return fmt.Errorf("module %q is on-demand — it is launched by its consumer, not supervised", id)
	}
	return s.launch(spec, restartMinDelay)
}

// launch starts one process instance and hands it to a monitor goroutine.
// delay is the restart backoff seed (restartMinDelay for a fresh start).
func (s *Supervisor) launch(spec ModuleSpec, delay time.Duration) error {
	s.mu.Lock()
	if _, running := s.running[spec.ID]; running {
		s.mu.Unlock()
		return fmt.Errorf("module %q is already running", spec.ID)
	}
	if s.closed.Load() {
		s.mu.Unlock()
		return fmt.Errorf("module supervisor is shut down")
	}
	s.mu.Unlock()

	cmd, err := s.mgr.buildCommand(s.ctx, spec)
	if err != nil {
		return err
	}
	stdout, stderr, err := openLogs(s.mgr.Dir(spec.ID))
	if err != nil {
		return err
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := s.runSetup(spec, stdout, stderr); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("module %q: setup failed: %w", spec.ID, err)
	}

	if err := isolation.Start(cmd); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("module %q: start failed: %w", spec.ID, err)
	}

	p := &proc{cmd: cmd, done: make(chan struct{})}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		_ = cmd.Process.Kill()
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("module supervisor is shut down")
	}
	s.running[spec.ID] = p
	s.mu.Unlock()

	st := s.mgr.loadState(spec.ID)
	st.PID = cmd.Process.Pid
	st.StartedAt = time.Now()
	_ = s.mgr.saveState(spec.ID, st)

	s.mgr.publish("module.started", map[string]any{"module": spec.ID, "pid": cmd.Process.Pid})

	s.wg.Add(1)
	go s.monitor(spec, p, stdout, stderr, delay)
	return nil
}

// runSetup executes a module's init/setup commands before the main process
// launches — InitArgs when the InitMarker path is absent (first run), then
// every SetupArgs entry. Commands run through isolation.Run (same sandbox
// posture as the module itself) and log into the module's own log files;
// the whole phase shares one bounded timeout.
func (s *Supervisor) runSetup(spec ModuleSpec, stdout, stderr *os.File) error {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	cmds, err := s.mgr.setupCommands(ctx, spec)
	if err != nil {
		return err
	}
	for i, cmd := range cmds {
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := isolation.Run(cmd); err != nil {
			// Report position + subcommand only — argv may carry a
			// secret field value that must not reach error strings.
			verb := "setup"
			if len(cmd.Args) > 1 {
				verb = cmd.Args[1]
			}
			return fmt.Errorf("command %d (%s): %w", i+1, verb, err)
		}
	}
	return nil
}

// monitor waits for one process instance, then — unless the stop was
// intentional or the supervisor is closing — sleeps out the backoff and
// launches a replacement, which gets its own monitor.
func (s *Supervisor) monitor(spec ModuleSpec, p *proc, stdout, stderr *os.File, delay time.Duration) {
	defer s.wg.Done()
	defer close(p.done)
	defer func() { _ = stdout.Close() }()
	defer func() { _ = stderr.Close() }()

	err := p.cmd.Wait()

	s.mu.Lock()
	// Only delete if this instance is still the registered one — a manual
	// Restart may already have replaced it.
	if s.running[spec.ID] == p {
		delete(s.running, spec.ID)
	}
	s.mu.Unlock()

	st := s.mgr.loadState(spec.ID)
	st.PID = 0
	st.LastExit = exitString(err)
	if p.stopping.Load() || s.closed.Load() {
		_ = s.mgr.saveState(spec.ID, st)
		s.mgr.publish("module.stopped", map[string]any{"module": spec.ID})
		return
	}
	st.Restarts++
	_ = s.mgr.saveState(spec.ID, st)

	s.mgr.publish("module.crashed", map[string]any{
		"module": spec.ID, "exit": st.LastExit, "restarts": st.Restarts,
	})
	logger.WarnCF("modules", "module exited, restarting",
		map[string]any{"module": spec.ID, "exit": st.LastExit, "delay": delay.String()})

	select {
	case <-time.After(jitter(delay)):
	case <-s.stopCh:
		return
	}

	next := delay * 2
	if time.Since(st.StartedAt) > restartResetAfter {
		next = restartMinDelay
	} else if next > restartMaxDelay {
		next = restartMaxDelay
	}
	if err := s.launch(spec, next); err != nil {
		logger.ErrorCF("modules", "module relaunch failed",
			map[string]any{"module": spec.ID, "error": err.Error()})
	}
}

// Stop terminates a running module and suppresses restart.
func (s *Supervisor) Stop(id string) error {
	s.mu.Lock()
	p, ok := s.running[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("module %q is not running", id)
	}
	p.stopping.Store(true)
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		return fmt.Errorf("module %q did not stop within 10s", id)
	}
	return nil
}

// Restart restarts a running (or stopped) daemon module.
func (s *Supervisor) Restart(id string) error {
	if s.IsRunning(id) {
		if err := s.Stop(id); err != nil {
			return err
		}
	}
	return s.Start(id)
}

// StopAll terminates every supervised module. Call before mesh teardown so
// modules never outlive the daemon that supervises them.
func (s *Supervisor) StopAll() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	close(s.stopCh)
	s.cancel()
	s.mu.Lock()
	procs := make([]*proc, 0, len(s.running))
	for _, p := range s.running {
		p.stopping.Store(true)
		procs = append(procs, p)
	}
	s.mu.Unlock()
	for _, p := range procs {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	}
	for _, p := range procs {
		<-p.done
	}
	s.wg.Wait()
}

// buildCommand resolves the binary, argv, env, and workdir for a module.
// Secret fields are injected here — they never pass through config.json.
func (m *Manager) buildCommand(ctx context.Context, spec ModuleSpec) (*exec.Cmd, error) {
	bin, err := m.resolveBinary(spec)
	if err != nil {
		return nil, err
	}

	values := m.resolvedFields(spec, true)
	var args []string
	for _, f := range spec.ConfigFields {
		if f.Arg == "" || values[f.Key] == "" {
			continue
		}
		if f.Flag {
			if isTruthy(values[f.Key]) {
				args = append(args, "--"+f.Arg)
			}
			continue
		}
		args = append(args, "--"+f.Arg+"="+values[f.Key])
	}
	args = append(args, expandArgv(spec.Run.ArgsTemplate, values)...)
	return m.commandBase(ctx, spec, bin, args, values)
}

// resolveBinary returns the module's executable path per its install method.
func (m *Manager) resolveBinary(spec ModuleSpec) (string, error) {
	switch spec.Install.Method {
	case "detect":
		return m.detectBinary(spec)
	case "github-release", "npm":
		bin := m.binaryPath(spec)
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("module %q binary not found at %s — run `rhizome module install %s`",
				spec.ID, bin, spec.ID)
		}
		return bin, nil
	default:
		return "", fmt.Errorf("module %q has no runnable binary (method %q)", spec.ID, spec.Install.Method)
	}
}

// expandArgv renders one argv template list against resolved field values;
// entries that expand to empty or still hold a placeholder are dropped.
func expandArgv(tmpls []string, values map[string]string) []string {
	var out []string
	for _, tmpl := range tmpls {
		arg := expand(tmpl, values)
		if arg == "" || strings.Contains(arg, "{") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// commandBase finishes building a *exec.Cmd for bin+argv: the process env
// (Run.Env templates + per-field Env mappings) and the module workdir.
// Shared by the main run command and init/setup invocations.
func (m *Manager) commandBase(
	ctx context.Context, spec ModuleSpec, bin string,
	args []string, values map[string]string,
) (*exec.Cmd, error) {
	//nolint:gosec // G204: launching catalog binaries is the module system's purpose.
	cmd := exec.CommandContext(ctx, bin, args...)
	env := append([]string{}, os.Environ()...)
	for k, tmpl := range spec.Run.Env {
		env = append(env, k+"="+expand(tmpl, values))
	}
	for _, f := range spec.ConfigFields {
		if f.Env != "" && values[f.Key] != "" {
			env = append(env, f.Env+"="+values[f.Key])
		}
	}
	cmd.Env = env

	dir := m.Dir(spec.ID)
	if spec.Run.Workdir != "" {
		dir = filepath.Join(dir, spec.Run.Workdir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	cmd.Dir = dir
	return cmd, nil
}

// setupCommands builds the pre-launch commands for a module: InitArgs when
// the InitMarker path is absent (first-run init), then every SetupArgs
// entry (idempotent writes applied on each launch so field changes take
// effect on restart). Returns nil when the module declares no setup work or
// the marker shows init already ran.
func (m *Manager) setupCommands(ctx context.Context, spec ModuleSpec) ([]*exec.Cmd, error) {
	if len(spec.Run.InitArgs) == 0 && len(spec.Run.SetupArgs) == 0 {
		return nil, nil
	}
	values := m.resolvedFields(spec, true)
	var argvs [][]string
	if len(spec.Run.InitArgs) > 0 {
		if spec.Run.InitMarker == "" {
			return nil, fmt.Errorf("module %q: init_args require init_marker", spec.ID)
		}
		marker := expand(spec.Run.InitMarker, values)
		if marker == "" || strings.Contains(marker, "{") {
			return nil, fmt.Errorf("module %q: init_marker did not resolve (%q)",
				spec.ID, spec.Run.InitMarker)
		}
		if _, err := os.Stat(marker); err == nil {
			// Marker exists — init already ran.
		} else if os.IsNotExist(err) {
			argvs = append(argvs, spec.Run.InitArgs)
		} else {
			return nil, err
		}
	}
	argvs = append(argvs, spec.Run.SetupArgs...)
	if len(argvs) == 0 {
		return nil, nil
	}
	bin, err := m.resolveBinary(spec)
	if err != nil {
		return nil, err
	}
	cmds := make([]*exec.Cmd, 0, len(argvs))
	for _, tmpls := range argvs {
		argv := expandArgv(tmpls, values)
		if len(argv) == 0 {
			continue
		}
		cmd, err := m.commandBase(ctx, spec, bin, argv, values)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// openLogs opens (rotating once when over cap) the module's stdout/stderr
// log files under <id>/logs/.
func openLogs(moduleDir string) (stdout, stderr *os.File, err error) {
	dir := filepath.Join(moduleDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	open := func(name string) (*os.File, error) {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
			_ = os.Rename(path, path+".old")
		}
		//nolint:gosec // G304: path is the module's own log file under the modules root.
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	stdout, err = open("stdout.log")
	if err != nil {
		return nil, nil, err
	}
	stderr, err = open("stderr.log")
	if err != nil {
		_ = stdout.Close()
		return nil, nil, err
	}
	return stdout, stderr, nil
}

func exitString(err error) string {
	if err == nil {
		return "exit 0"
	}
	return err.Error()
}

func jitter(d time.Duration) time.Duration {
	//nolint:gosec // backoff jitter does not need crypto-random
	return d + time.Duration(rand.Int63n(int64(d/5)+1)) - d/10
}
