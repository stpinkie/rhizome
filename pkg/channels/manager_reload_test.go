package channels

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
)

// reloadProbe records what happened to one channel instance created by
// reloadFactory.
type reloadProbe struct {
	ch      *mockChannel
	starts  atomic.Int32
	sent    chan bus.OutboundMessage
	stopped chan struct{}
}

// reloadFactory builds mockChannel instances for Reload tests and keeps every
// instance it created, so a test can tell the old instance from the new one.
type reloadFactory struct {
	mu      sync.Mutex
	failing map[string]bool
	probes  map[string][]*reloadProbe
}

// useReloadFactory registers a reloadFactory for channelType and restores the
// previous registry entry when the test ends.
func useReloadFactory(t *testing.T, channelType string) *reloadFactory {
	t.Helper()
	f := &reloadFactory{
		failing: make(map[string]bool),
		probes:  make(map[string][]*reloadProbe),
	}
	factoriesMu.Lock()
	prev, hadPrev := factories[channelType]
	factories[channelType] = f.create
	factoriesMu.Unlock()
	t.Cleanup(func() {
		factoriesMu.Lock()
		defer factoriesMu.Unlock()
		if hadPrev {
			factories[channelType] = prev
		} else {
			delete(factories, channelType)
		}
	})
	return f
}

func (f *reloadFactory) setFailing(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[name] = true
}

func (f *reloadFactory) create(name, _ string, _ *config.Config, _ *bus.MessageBus) (Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing[name] {
		return nil, fmt.Errorf("factory failure for %s", name)
	}
	p := &reloadProbe{
		sent:    make(chan bus.OutboundMessage, 8),
		stopped: make(chan struct{}),
	}
	var stopOnce sync.Once
	p.ch = &mockChannel{
		startFn: func(context.Context) error {
			p.starts.Add(1)
			return nil
		},
		sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
			select {
			case p.sent <- msg:
			default:
			}
			return nil
		},
		stopFn: func(context.Context) error {
			stopOnce.Do(func() { close(p.stopped) })
			return nil
		},
	}
	f.probes[name] = append(f.probes[name], p)
	return p.ch, nil
}

// instances returns the probes created for the named channel, oldest first.
func (f *reloadFactory) instances(name string) []*reloadProbe {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*reloadProbe(nil), f.probes[name]...)
}

// maixcamTestChannel returns a channel config that passes the readiness check
// without any secrets. Different ports produce different config hashes.
func maixcamTestChannel(port int) *config.Channel {
	return &config.Channel{
		Enabled:  true,
		Type:     config.ChannelMaixCam,
		Settings: config.RawNode(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port)),
	}
}

// unreadyTelegramChannel returns an enabled telegram config without a token.
// It gets a config hash but initChannels skips it, so it never reaches
// m.channels.
func unreadyTelegramChannel(baseURL string) *config.Channel {
	return &config.Channel{
		Enabled:  true,
		Type:     config.ChannelTelegram,
		Settings: config.RawNode(fmt.Sprintf(`{"base_url":%q}`, baseURL)),
	}
}

func reloadTestConfig(t *testing.T, channels config.ChannelsConfig) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Channels = channels
	if err := config.InitChannelList(cfg.Channels); err != nil {
		t.Fatalf("InitChannelList() error = %v", err)
	}
	return cfg
}

func startReloadTestManager(t *testing.T, cfg *config.Config) *Manager {
	t.Helper()
	m, err := NewManager(cfg, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := m.StartAll(t.Context()); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	return m
}

func stopReloadTestManager(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.StopAll(ctx); err != nil {
		t.Errorf("StopAll() error = %v", err)
	}
}

// reloadWithoutPanic turns a panic in Reload into a test failure, so one broken
// case does not take down the whole test binary.
func reloadWithoutPanic(t *testing.T, m *Manager, cfg *config.Config) error {
	t.Helper()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Reload() panicked: %v", r)
			}
		}()
		err = m.Reload(t.Context(), cfg)
	}()
	return err
}

func workerFor(m *Manager, name string) *channelWorker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workers[name]
}

func publishTestOutbound(t *testing.T, m *Manager, channel, content string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := m.bus.PublishOutbound(ctx, testOutboundMessage(bus.OutboundMessage{
		Channel: channel,
		ChatID:  "chat-1",
		Content: content,
	})); err != nil {
		t.Fatalf("PublishOutbound() error = %v", err)
	}
}

func waitProbeDelivery(t *testing.T, p *reloadProbe, content string) {
	t.Helper()
	select {
	case msg := <-p.sent:
		if msg.Content != content {
			t.Fatalf("delivered content = %q, want %q", msg.Content, content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("message %q was not delivered", content)
	}
}

func TestReload_ChangedChannelReplacesWorker(t *testing.T) {
	f := useReloadFactory(t, config.ChannelMaixCam)
	m := startReloadTestManager(t, reloadTestConfig(t, config.ChannelsConfig{
		"cam": maixcamTestChannel(1),
	}))
	defer stopReloadTestManager(t, m)

	oldWorker := workerFor(m, "cam")
	if oldWorker == nil {
		t.Fatal("expected a worker for cam after StartAll")
	}

	err := reloadWithoutPanic(t, m, reloadTestConfig(t, config.ChannelsConfig{
		"cam": maixcamTestChannel(2),
	}))
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	probes := f.instances("cam")
	if len(probes) != 2 {
		t.Fatalf("factory created %d cam instances, want 2", len(probes))
	}
	oldProbe, newProbe := probes[0], probes[1]

	select {
	case <-oldProbe.stopped:
	default:
		t.Error("old cam instance was not stopped")
	}
	select {
	case <-oldWorker.done:
	case <-time.After(time.Second):
		t.Error("old cam worker did not exit")
	}

	if ch, _ := m.GetChannel("cam"); ch != Channel(newProbe.ch) {
		t.Fatal("channel cam is not the instance created by Reload")
	}
	if w := workerFor(m, "cam"); w == nil || w == oldWorker {
		t.Fatalf("worker for cam = %p, want a new worker (old %p)", w, oldWorker)
	}

	publishTestOutbound(t, m, "cam", "after reload")
	waitProbeDelivery(t, newProbe, "after reload")
	select {
	case msg := <-oldProbe.sent:
		t.Fatalf("old cam instance received %q after reload", msg.Content)
	default:
	}
}

func TestReload_RemovedUninitializedChannelNoPanic(t *testing.T) {
	tests := []struct {
		name     string
		telegram *config.Channel
	}{
		{
			name:     "disabled",
			telegram: &config.Channel{Enabled: false, Type: config.ChannelTelegram},
		},
		{
			name:     "changed but still not ready",
			telegram: unreadyTelegramChannel("http://127.0.0.1:2"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useReloadFactory(t, config.ChannelMaixCam)
			m := startReloadTestManager(t, reloadTestConfig(t, config.ChannelsConfig{
				"cam":      maixcamTestChannel(1),
				"telegram": unreadyTelegramChannel("http://127.0.0.1:1"),
			}))
			defer stopReloadTestManager(t, m)

			if _, ok := m.GetChannel("telegram"); ok {
				t.Fatal("telegram without token should not be initialized")
			}
			camWorker := workerFor(m, "cam")

			err := reloadWithoutPanic(t, m, reloadTestConfig(t, config.ChannelsConfig{
				"cam":      maixcamTestChannel(1),
				"telegram": tt.telegram,
			}))
			if err != nil {
				t.Fatalf("Reload() error = %v", err)
			}

			if _, ok := m.GetChannel("telegram"); ok {
				t.Fatal("telegram without token should still not be initialized")
			}
			if w := workerFor(m, "telegram"); w != nil {
				t.Fatal("did not expect a worker for telegram")
			}
			if w := workerFor(m, "cam"); w != camWorker {
				t.Fatal("unchanged cam channel should keep its worker")
			}
		})
	}
}

func TestReload_AddedChannelInitFailureNoPanic(t *testing.T) {
	tests := []struct {
		name    string
		failing string
		next    config.ChannelsConfig
		broken  string
	}{
		{
			name:    "new channel factory error",
			failing: "extra",
			next: config.ChannelsConfig{
				"keep":  maixcamTestChannel(1),
				"cam":   maixcamTestChannel(2),
				"extra": maixcamTestChannel(3),
			},
			broken: "extra",
		},
		{
			name: "new channel not ready",
			next: config.ChannelsConfig{
				"keep":     maixcamTestChannel(1),
				"cam":      maixcamTestChannel(2),
				"telegram": unreadyTelegramChannel("http://127.0.0.1:1"),
			},
			broken: "telegram",
		},
		{
			name:    "changed channel factory error",
			failing: "cam",
			next: config.ChannelsConfig{
				"keep": maixcamTestChannel(1),
				"cam":  maixcamTestChannel(4),
			},
			broken: "cam",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := useReloadFactory(t, config.ChannelMaixCam)
			m := startReloadTestManager(t, reloadTestConfig(t, config.ChannelsConfig{
				"keep": maixcamTestChannel(1),
				"cam":  maixcamTestChannel(2),
			}))
			defer stopReloadTestManager(t, m)

			if tt.failing != "" {
				f.setFailing(tt.failing)
			}
			if err := reloadWithoutPanic(t, m, reloadTestConfig(t, tt.next)); err != nil {
				t.Fatalf("Reload() error = %v", err)
			}

			for _, p := range f.instances(tt.broken) {
				if n := p.starts.Load(); n > 1 {
					t.Fatalf("%s instance started %d times, want at most once", tt.broken, n)
				}
			}
			if _, ok := m.GetChannel(tt.broken); ok {
				t.Fatalf("did not expect channel %s after failed initialization", tt.broken)
			}
			if w := workerFor(m, tt.broken); w != nil {
				t.Fatalf("did not expect a worker for %s", tt.broken)
			}

			keep := f.instances("keep")
			if len(keep) != 1 {
				t.Fatalf("factory created %d keep instances, want 1", len(keep))
			}
			publishTestOutbound(t, m, "keep", "still delivered")
			waitProbeDelivery(t, keep[0], "still delivered")
		})
	}
}

func TestReload_KeepsDispatchTask(t *testing.T) {
	useReloadFactory(t, config.ChannelMaixCam)
	m := startReloadTestManager(t, reloadTestConfig(t, config.ChannelsConfig{
		"cam": maixcamTestChannel(1),
	}))
	task := m.dispatchTask
	if task == nil {
		t.Fatal("expected StartAll to create a dispatch task")
	}

	err := reloadWithoutPanic(t, m, reloadTestConfig(t, config.ChannelsConfig{
		"cam":   maixcamTestChannel(1),
		"extra": maixcamTestChannel(2),
	}))
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if m.dispatchTask != task {
		t.Error("Reload replaced the dispatch task created by StartAll")
	}
	extraWorker := workerFor(m, "extra")
	if extraWorker == nil {
		t.Fatal("expected Reload to start a worker for extra")
	}

	stopReloadTestManager(t, m)
	select {
	case <-extraWorker.done:
	case <-time.After(time.Second):
		t.Fatal("worker started by Reload is still running after StopAll")
	}
}
