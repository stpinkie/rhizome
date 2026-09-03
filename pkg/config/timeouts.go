package config

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	providercommon "github.com/stpinkie/rhizome/pkg/providers/common"
)

// Duration is a time.Duration that marshals to and from human-readable strings
// (e.g. "120s", "5m") in JSON/config files and env vars. Zero means "not set".
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d *Duration) MarshalJSON() ([]byte, error) {
	if d == nil {
		return json.Marshal("0s")
	}
	return json.Marshal(time.Duration(*d).String())
}

// UnmarshalJSON implements json.Unmarshaler. It accepts a duration string or a
// numeric nanosecond count for backward compatibility.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		pd, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(pd)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*d = Duration(n)
		return nil
	}
	return fmt.Errorf("invalid duration: %s", string(data))
}

// MarshalText implements encoding.TextMarshaler for env var parsing.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for env var parsing.
func (d *Duration) UnmarshalText(data []byte) error {
	pd, err := time.ParseDuration(string(data))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(data), err)
	}
	*d = Duration(pd)
	return nil
}

func (d Duration) Duration() time.Duration { return time.Duration(d) }

var (
	_ encoding.TextMarshaler   = Duration(0)
	_ encoding.TextUnmarshaler = (*Duration)(nil)
)

// TimeoutsConfig groups all user-configurable wait times. A zero value in any
// leaf means "use the built-in default". Existing per-component timeout fields
// (e.g. ModelConfig.RequestTimeout) take precedence over these global values.
type TimeoutsConfig struct {
	LLM       LLMTimeouts       `json:"llm,omitempty"`
	Tools     ToolTimeouts      `json:"tools,omitempty"`
	Agent     AgentTimeouts     `json:"agent,omitempty"`
	Mesh      MeshTimeouts      `json:"mesh,omitempty"`
	Network   NetworkTimeouts   `json:"network,omitempty"`
	Sync      SyncTimeouts      `json:"sync,omitempty"`
	HTTP      HTTPTimeouts      `json:"http,omitempty"`
	Media     MediaTimeouts     `json:"media,omitempty"`
	Gateway   GatewayTimeouts   `json:"gateway,omitempty"`
	Cron      CronTimeouts      `json:"cron,omitempty"`
	Evolution EvolutionTimeouts `json:"evolution,omitempty"`
	Health    HealthTimeouts    `json:"health,omitempty"`
	Heartbeat HeartbeatTimeouts `json:"heartbeat,omitempty"`
	Updater   UpdaterTimeouts   `json:"updater,omitempty"`
	Channel   ChannelTimeouts   `json:"channel,omitempty"`
}

// LLMTimeouts covers LLM provider calls and streaming.
type LLMTimeouts struct {
	RequestSeconds        Duration `json:"request_seconds,omitempty"         env:"RHIZOME_TIMEOUTS_LLM_REQUEST_SECONDS"`
	StreamingIdle         Duration `json:"streaming_idle,omitempty"          env:"RHIZOME_TIMEOUTS_LLM_STREAMING_IDLE"`
	ProviderInit          Duration `json:"provider_init,omitempty"           env:"RHIZOME_TIMEOUTS_LLM_PROVIDER_INIT"`
	FirstTokenTimeout     Duration `json:"first_token_timeout,omitempty"     env:"RHIZOME_TIMEOUTS_LLM_FIRST_TOKEN_TIMEOUT"`
	CredentialCacheTTL    Duration `json:"credential_cache_ttl,omitempty"    env:"RHIZOME_TIMEOUTS_LLM_CREDENTIAL_CACHE_TTL"`
	CooldownFailureWindow Duration `json:"cooldown_failure_window,omitempty" env:"RHIZOME_TIMEOUTS_LLM_COOLDOWN_FAILURE_WINDOW"`
}

// ToolTimeouts covers shell/cron/web tool execution and session cleanup.
type ToolTimeouts struct {
	ExecSeconds            Duration `json:"exec_seconds,omitempty"             env:"RHIZOME_TIMEOUTS_TOOLS_EXEC_SECONDS"`
	CronExecMinutes        Duration `json:"cron_exec_minutes,omitempty"        env:"RHIZOME_TIMEOUTS_TOOLS_CRON_EXEC_MINUTES"`
	WebSearchSeconds       Duration `json:"web_search_seconds,omitempty"       env:"RHIZOME_TIMEOUTS_TOOLS_WEB_SEARCH_SECONDS"`
	WebPerplexitySeconds   Duration `json:"web_perplexity_seconds,omitempty"   env:"RHIZOME_TIMEOUTS_TOOLS_WEB_PERPLEXITY_SECONDS"`
	WebFetchSeconds        Duration `json:"web_fetch_seconds,omitempty"        env:"RHIZOME_TIMEOUTS_TOOLS_WEB_FETCH_SECONDS"`
	SessionCleanupInterval Duration `json:"session_cleanup_interval,omitempty" env:"RHIZOME_TIMEOUTS_TOOLS_SESSION_CLEANUP_INTERVAL"`
	SessionCleanupAge      Duration `json:"session_cleanup_age,omitempty"      env:"RHIZOME_TIMEOUTS_TOOLS_SESSION_CLEANUP_AGE"`
	ProcessReapGrace       Duration `json:"process_reap_grace,omitempty"       env:"RHIZOME_TIMEOUTS_TOOLS_PROCESS_REAP_GRACE"`
	SerialPollInterval     Duration `json:"serial_poll_interval,omitempty"     env:"RHIZOME_TIMEOUTS_TOOLS_SERIAL_POLL_INTERVAL"`
}

// AgentTimeouts covers agent/subturn runtime timing.
type AgentTimeouts struct {
	SubTurnDefaultMinutes     Duration `json:"subturn_default_minutes,omitempty"     env:"RHIZOME_TIMEOUTS_AGENT_SUBTURN_DEFAULT_MINUTES"`
	SubTurnConcurrencySeconds Duration `json:"subturn_concurrency_seconds,omitempty" env:"RHIZOME_TIMEOUTS_AGENT_SUBTURN_CONCURRENCY_SECONDS"`
	ProviderReloadGrace       Duration `json:"provider_reload_grace,omitempty"       env:"RHIZOME_TIMEOUTS_AGENT_PROVIDER_RELOAD_GRACE"`
	IdleLoopInterval          Duration `json:"idle_loop_interval,omitempty"          env:"RHIZOME_TIMEOUTS_AGENT_IDLE_LOOP_INTERVAL"`
}

// MeshTimeouts covers P2P mesh remote calls and capability advertisement.
type MeshTimeouts struct {
	RemoteCall          Duration `json:"remote_call,omitempty"          env:"RHIZOME_TIMEOUTS_MESH_REMOTE_CALL"`
	CapabilityAdvertise Duration `json:"capability_advertise,omitempty" env:"RHIZOME_TIMEOUTS_MESH_CAPABILITY_ADVERTISE"`
	ProtocolNegotiation Duration `json:"protocol_negotiation,omitempty" env:"RHIZOME_TIMEOUTS_MESH_PROTOCOL_NEGOTIATION"`
}

// NetworkTimeouts covers libp2p listener, bootstrap, ping, and DHT timing.
type NetworkTimeouts struct {
	ListenerReady        Duration `json:"listener_ready,omitempty"         env:"RHIZOME_TIMEOUTS_NETWORK_LISTENER_READY"`
	BootstrapAttempts    int      `json:"bootstrap_attempts,omitempty"     env:"RHIZOME_TIMEOUTS_NETWORK_BOOTSTRAP_ATTEMPTS"`
	BootstrapBackoff     Duration `json:"bootstrap_backoff,omitempty"      env:"RHIZOME_TIMEOUTS_NETWORK_BOOTSTRAP_BACKOFF"`
	Ping                 Duration `json:"ping,omitempty"                   env:"RHIZOME_TIMEOUTS_NETWORK_PING"`
	DHTDial              Duration `json:"dht_dial,omitempty"               env:"RHIZOME_TIMEOUTS_NETWORK_DHT_DIAL"`
	DHTQuery             Duration `json:"dht_query,omitempty"              env:"RHIZOME_TIMEOUTS_NETWORK_DHT_QUERY"`
	DHTRetry             Duration `json:"dht_retry,omitempty"              env:"RHIZOME_TIMEOUTS_NETWORK_DHT_RETRY"`
	DHTReprovideInterval Duration `json:"dht_reprovide_interval,omitempty" env:"RHIZOME_TIMEOUTS_NETWORK_DHT_REPROVIDE_INTERVAL"`
}

// SyncTimeouts covers workspace git sync timing.
type SyncTimeouts struct {
	CommitInterval      Duration `json:"commit_interval,omitempty"       env:"RHIZOME_TIMEOUTS_SYNC_COMMIT_INTERVAL"`
	AnnounceInterval    Duration `json:"announce_interval,omitempty"     env:"RHIZOME_TIMEOUTS_SYNC_ANNOUNCE_INTERVAL"`
	PeerWait            Duration `json:"peer_wait,omitempty"             env:"RHIZOME_TIMEOUTS_SYNC_PEER_WAIT"`
	FetchRetry          Duration `json:"fetch_retry,omitempty"           env:"RHIZOME_TIMEOUTS_SYNC_FETCH_RETRY"`
	FetchAttemptTimeout Duration `json:"fetch_attempt_timeout,omitempty" env:"RHIZOME_TIMEOUTS_SYNC_FETCH_ATTEMPT_TIMEOUT"`
	TransportRequest    Duration `json:"transport_request,omitempty"     env:"RHIZOME_TIMEOUTS_SYNC_TRANSPORT_REQUEST"`
	TransportPackfile   Duration `json:"transport_packfile,omitempty"    env:"RHIZOME_TIMEOUTS_SYNC_TRANSPORT_PACKFILE"`
	WatcherDebounce     Duration `json:"watcher_debounce,omitempty"      env:"RHIZOME_TIMEOUTS_SYNC_WATCHER_DEBOUNCE"`
}

// HTTPTimeouts covers shared HTTP client, dialer, and retry defaults.
type HTTPTimeouts struct {
	Request        Duration `json:"request,omitempty"          env:"RHIZOME_TIMEOUTS_HTTP_REQUEST"`
	IdleConn       Duration `json:"idle_conn,omitempty"        env:"RHIZOME_TIMEOUTS_HTTP_IDLE_CONN"`
	TLSHandshake   Duration `json:"tls_handshake,omitempty"    env:"RHIZOME_TIMEOUTS_HTTP_TLS_HANDSHAKE"`
	Dial           Duration `json:"dial,omitempty"             env:"RHIZOME_TIMEOUTS_HTTP_DIAL"`
	KeepAlive      Duration `json:"keepalive,omitempty"        env:"RHIZOME_TIMEOUTS_HTTP_KEEPALIVE"`
	RetryDelayUnit Duration `json:"retry_delay_unit,omitempty" env:"RHIZOME_TIMEOUTS_HTTP_RETRY_DELAY_UNIT"`
	MaxRetrySleep  Duration `json:"max_retry_sleep,omitempty"  env:"RHIZOME_TIMEOUTS_HTTP_MAX_RETRY_SLEEP"`
	MaxRetries     int      `json:"max_retries,omitempty"      env:"RHIZOME_TIMEOUTS_HTTP_MAX_RETRIES"`
	MaxRedirects   int      `json:"max_redirects,omitempty"    env:"RHIZOME_TIMEOUTS_HTTP_MAX_REDIRECTS"`
}

// MediaTimeouts covers media download and cleanup timing.
type MediaTimeouts struct {
	Download        Duration `json:"download,omitempty"         env:"RHIZOME_TIMEOUTS_MEDIA_DOWNLOAD"`
	MaxAge          Duration `json:"max_age,omitempty"          env:"RHIZOME_TIMEOUTS_MEDIA_MAX_AGE"`
	CleanupInterval Duration `json:"cleanup_interval,omitempty" env:"RHIZOME_TIMEOUTS_MEDIA_CLEANUP_INTERVAL"`
}

// GatewayTimeouts covers daemon shutdown and health check timing.
type GatewayTimeouts struct {
	ServiceShutdown      Duration `json:"service_shutdown,omitempty"       env:"RHIZOME_TIMEOUTS_GATEWAY_SERVICE_SHUTDOWN"`
	ProviderReload       Duration `json:"provider_reload,omitempty"        env:"RHIZOME_TIMEOUTS_GATEWAY_PROVIDER_RELOAD"`
	GracefulShutdown     Duration `json:"graceful_shutdown,omitempty"      env:"RHIZOME_TIMEOUTS_GATEWAY_GRACEFUL_SHUTDOWN"`
	HealthCheckInterval  Duration `json:"health_check_interval,omitempty"  env:"RHIZOME_TIMEOUTS_GATEWAY_HEALTH_CHECK_INTERVAL"`
	ConfigReloadInterval Duration `json:"config_reload_interval,omitempty" env:"RHIZOME_TIMEOUTS_GATEWAY_CONFIG_RELOAD_INTERVAL"`
	ConfigReloadDebounce Duration `json:"config_reload_debounce,omitempty" env:"RHIZOME_TIMEOUTS_GATEWAY_CONFIG_RELOAD_DEBOUNCE"`
}

// CronTimeouts covers the cron service scheduler.
type CronTimeouts struct {
	NextWakeResolution Duration `json:"next_wake_resolution,omitempty" env:"RHIZOME_TIMEOUTS_CRON_NEXT_WAKE_RESOLUTION"`
	HeartbeatInitial   Duration `json:"heartbeat_initial,omitempty"    env:"RHIZOME_TIMEOUTS_CRON_HEARTBEAT_INITIAL"`
}

// EvolutionTimeouts covers the evolution cold-path LLM calls.
type EvolutionTimeouts struct {
	TaskSuccessJudge Duration `json:"task_success_judge,omitempty" env:"RHIZOME_TIMEOUTS_EVOLUTION_TASK_SUCCESS_JUDGE"`
	PatternCluster   Duration `json:"pattern_cluster,omitempty"    env:"RHIZOME_TIMEOUTS_EVOLUTION_PATTERN_CLUSTER"`
	DraftGeneration  Duration `json:"draft_generation,omitempty"   env:"RHIZOME_TIMEOUTS_EVOLUTION_DRAFT_GENERATION"`
	SkillCold        Duration `json:"skill_cold,omitempty"         env:"RHIZOME_TIMEOUTS_EVOLUTION_SKILL_COLD"`
	SkillArchived    Duration `json:"skill_archived,omitempty"     env:"RHIZOME_TIMEOUTS_EVOLUTION_SKILL_ARCHIVED"`
	SkillDeleted     Duration `json:"skill_deleted,omitempty"      env:"RHIZOME_TIMEOUTS_EVOLUTION_SKILL_DELETED"`
}

// HealthTimeouts covers the health check HTTP server timeouts.
type HealthTimeouts struct {
	Read  Duration `json:"read,omitempty"  env:"RHIZOME_TIMEOUTS_HEALTH_READ"`
	Write Duration `json:"write,omitempty" env:"RHIZOME_TIMEOUTS_HEALTH_WRITE"`
}

// HeartbeatTimeouts covers the heartbeat service scheduling.
type HeartbeatTimeouts struct {
	Interval       Duration `json:"interval,omitempty"        env:"RHIZOME_TIMEOUTS_HEARTBEAT_INTERVAL"`
	InitialDelay   Duration `json:"initial_delay,omitempty"   env:"RHIZOME_TIMEOUTS_HEARTBEAT_INITIAL_DELAY"`
	PublishTimeout Duration `json:"publish_timeout,omitempty" env:"RHIZOME_TIMEOUTS_HEARTBEAT_PUBLISH_TIMEOUT"`
}

// UpdaterTimeouts covers the self-updater download/throttle timing.
type UpdaterTimeouts struct {
	Download     Duration `json:"download,omitempty"      env:"RHIZOME_TIMEOUTS_UPDATER_DOWNLOAD"`
	ProgressTick Duration `json:"progress_tick,omitempty" env:"RHIZOME_TIMEOUTS_UPDATER_PROGRESS_TICK"`
}

// ChannelTimeouts covers shared channel (messaging) operation timeouts.
type ChannelTimeouts struct {
	RequestTimeout        Duration `json:"request,omitempty"                 env:"RHIZOME_TIMEOUTS_CHANNEL_REQUEST"`
	ConnectTimeout        Duration `json:"connect,omitempty"                 env:"RHIZOME_TIMEOUTS_CHANNEL_CONNECT"`
	CommandTimeout        Duration `json:"command,omitempty"                 env:"RHIZOME_TIMEOUTS_CHANNEL_COMMAND"`
	AuthTimeout           Duration `json:"auth,omitempty"                    env:"RHIZOME_TIMEOUTS_CHANNEL_AUTH"`
	MediaTimeout          Duration `json:"media,omitempty"                   env:"RHIZOME_TIMEOUTS_CHANNEL_MEDIA"`
	PublishTimeout        Duration `json:"publish,omitempty"                 env:"RHIZOME_TIMEOUTS_CHANNEL_PUBLISH"`
	HeartbeatInterval     Duration `json:"heartbeat_interval,omitempty"      env:"RHIZOME_TIMEOUTS_CHANNEL_HEARTBEAT_INTERVAL"`
	ReconnectInitial      Duration `json:"reconnect_initial,omitempty"       env:"RHIZOME_TIMEOUTS_CHANNEL_RECONNECT_INITIAL"`
	ReconnectMax          Duration `json:"reconnect_max,omitempty"           env:"RHIZOME_TIMEOUTS_CHANNEL_RECONNECT_MAX"`
	MessageCacheTTL       Duration `json:"message_cache_ttl,omitempty"       env:"RHIZOME_TIMEOUTS_CHANNEL_MESSAGE_CACHE_TTL"`
	ConfigCacheTTL        Duration `json:"config_cache_ttl,omitempty"        env:"RHIZOME_TIMEOUTS_CHANNEL_CONFIG_CACHE_TTL"`
	SessionPauseDuration  Duration `json:"session_pause_duration,omitempty"  env:"RHIZOME_TIMEOUTS_CHANNEL_SESSION_PAUSE_DURATION"`
	MediaGroupDelay       Duration `json:"media_group_delay,omitempty"       env:"RHIZOME_TIMEOUTS_CHANNEL_MEDIA_GROUP_DELAY"`
	TypingMaxDuration     Duration `json:"typing_max_duration,omitempty"     env:"RHIZOME_TIMEOUTS_CHANNEL_TYPING_MAX_DURATION"`
	ToolFeedbackInterval  Duration `json:"tool_feedback_interval,omitempty"  env:"RHIZOME_TIMEOUTS_CHANNEL_TOOL_FEEDBACK_INTERVAL"`
	StreamMaxDuration     Duration `json:"stream_max_duration,omitempty"     env:"RHIZOME_TIMEOUTS_CHANNEL_STREAM_MAX_DURATION"`
	StreamMinInterval     Duration `json:"stream_min_interval,omitempty"     env:"RHIZOME_TIMEOUTS_CHANNEL_STREAM_MIN_INTERVAL"`
	RouteTTL              Duration `json:"route_ttl,omitempty"               env:"RHIZOME_TIMEOUTS_CHANNEL_ROUTE_TTL"`
	PollInterval          Duration `json:"poll_interval,omitempty"           env:"RHIZOME_TIMEOUTS_CHANNEL_POLL_INTERVAL"`
	RateLimitDelay        Duration `json:"rate_limit_delay,omitempty"        env:"RHIZOME_TIMEOUTS_CHANNEL_RATE_LIMIT_DELAY"`
	MaxBackoff            Duration `json:"max_backoff,omitempty"             env:"RHIZOME_TIMEOUTS_CHANNEL_MAX_BACKOFF"`
	JanitorInterval       Duration `json:"janitor_interval,omitempty"        env:"RHIZOME_TIMEOUTS_CHANNEL_JANITOR_INTERVAL"`
	PlaceholderTTL        Duration `json:"placeholder_ttl,omitempty"         env:"RHIZOME_TIMEOUTS_CHANNEL_PLACEHOLDER_TTL"`
	TypingRefreshInterval Duration `json:"typing_refresh_interval,omitempty" env:"RHIZOME_TIMEOUTS_CHANNEL_TYPING_REFRESH_INTERVAL"`
}

// DefaultTimeouts returns the built-in timeout values that match the current
// hardcoded defaults in the codebase.
func DefaultTimeouts() TimeoutsConfig {
	return TimeoutsConfig{
		LLM: LLMTimeouts{
			RequestSeconds:        Duration(120 * time.Second),
			StreamingIdle:         Duration(5 * time.Minute),
			ProviderInit:          Duration(30 * time.Second),
			FirstTokenTimeout:     0,
			CredentialCacheTTL:    Duration(time.Hour),
			CooldownFailureWindow: Duration(24 * time.Hour),
		},
		Tools: ToolTimeouts{
			ExecSeconds:            Duration(60 * time.Second),
			CronExecMinutes:        Duration(5 * time.Minute),
			WebSearchSeconds:       Duration(10 * time.Second),
			WebPerplexitySeconds:   Duration(30 * time.Second),
			WebFetchSeconds:        Duration(60 * time.Second),
			SessionCleanupInterval: Duration(5 * time.Minute),
			SessionCleanupAge:      Duration(30 * time.Minute),
			ProcessReapGrace:       Duration(2 * time.Second),
			SerialPollInterval:     Duration(100 * time.Millisecond),
		},
		Agent: AgentTimeouts{
			SubTurnDefaultMinutes:     Duration(5 * time.Minute),
			SubTurnConcurrencySeconds: Duration(30 * time.Second),
			ProviderReloadGrace:       Duration(30 * time.Second),
			IdleLoopInterval:          Duration(100 * time.Millisecond),
		},
		Mesh: MeshTimeouts{
			RemoteCall:          Duration(5 * time.Minute),
			CapabilityAdvertise: Duration(5 * time.Minute),
			ProtocolNegotiation: Duration(5 * time.Second),
		},
		Network: NetworkTimeouts{
			ListenerReady:        Duration(2 * time.Second),
			BootstrapAttempts:    3,
			BootstrapBackoff:     Duration(250 * time.Millisecond),
			Ping:                 Duration(5 * time.Second),
			DHTDial:              Duration(15 * time.Second),
			DHTQuery:             Duration(60 * time.Second),
			DHTRetry:             Duration(5 * time.Second),
			DHTReprovideInterval: Duration(10 * time.Minute),
		},
		Sync: SyncTimeouts{
			CommitInterval:      Duration(2 * time.Second),
			AnnounceInterval:    Duration(30 * time.Second),
			PeerWait:            Duration(10 * time.Second),
			FetchRetry:          Duration(300 * time.Millisecond),
			FetchAttemptTimeout: Duration(60 * time.Second),
			TransportRequest:    Duration(30 * time.Second),
			TransportPackfile:   Duration(60 * time.Second),
			WatcherDebounce:     Duration(2 * time.Second),
		},
		HTTP: HTTPTimeouts{
			Request:        Duration(providercommon.DefaultRequestTimeout),
			IdleConn:       Duration(30 * time.Second),
			TLSHandshake:   Duration(15 * time.Second),
			Dial:           Duration(15 * time.Second),
			KeepAlive:      Duration(30 * time.Second),
			RetryDelayUnit: Duration(time.Second),
			MaxRetrySleep:  Duration(time.Minute),
			MaxRetries:     3,
			MaxRedirects:   10,
		},
		Media: MediaTimeouts{
			Download:        Duration(60 * time.Second),
			MaxAge:          Duration(15 * time.Minute),
			CleanupInterval: Duration(5 * time.Minute),
		},
		Cron: CronTimeouts{
			NextWakeResolution: Duration(time.Hour),
			HeartbeatInitial:   Duration(time.Second),
		},
		Evolution: EvolutionTimeouts{
			TaskSuccessJudge: Duration(15 * time.Second),
			PatternCluster:   Duration(45 * time.Second),
			DraftGeneration:  Duration(60 * time.Second),
			SkillCold:        Duration(90 * 24 * time.Hour),
			SkillArchived:    Duration(180 * 24 * time.Hour),
			SkillDeleted:     Duration(365 * 24 * time.Hour),
		},
		Health: HealthTimeouts{
			Read:  Duration(5 * time.Second),
			Write: Duration(5 * time.Second),
		},
		Heartbeat: HeartbeatTimeouts{
			Interval:       Duration(30 * time.Minute),
			InitialDelay:   Duration(time.Second),
			PublishTimeout: Duration(5 * time.Second),
		},
		Updater: UpdaterTimeouts{
			Download:     Duration(2 * time.Minute),
			ProgressTick: Duration(200 * time.Millisecond),
		},
		Channel: ChannelTimeouts{
			RequestTimeout:        Duration(30 * time.Second),
			ConnectTimeout:        Duration(15 * time.Second),
			CommandTimeout:        Duration(10 * time.Second),
			AuthTimeout:           Duration(5 * time.Minute),
			MediaTimeout:          Duration(30 * time.Second),
			PublishTimeout:        Duration(5 * time.Second),
			HeartbeatInterval:     Duration(30 * time.Second),
			ReconnectInitial:      Duration(5 * time.Second),
			ReconnectMax:          Duration(5 * time.Minute),
			MessageCacheTTL:       Duration(30 * time.Second),
			ConfigCacheTTL:        Duration(24 * time.Hour),
			SessionPauseDuration:  Duration(time.Hour),
			MediaGroupDelay:       Duration(500 * time.Millisecond),
			TypingMaxDuration:     Duration(5 * time.Minute),
			ToolFeedbackInterval:  Duration(3 * time.Second),
			StreamMaxDuration:     Duration(5*time.Minute + 30*time.Second),
			StreamMinInterval:     Duration(500 * time.Millisecond),
			RouteTTL:              Duration(30 * time.Minute),
			PollInterval:          Duration(2 * time.Second),
			RateLimitDelay:        Duration(1 * time.Second),
			MaxBackoff:            Duration(8 * time.Second),
			JanitorInterval:       Duration(10 * time.Second),
			PlaceholderTTL:        Duration(10 * time.Minute),
			TypingRefreshInterval: Duration(20 * time.Second),
		},
		Gateway: GatewayTimeouts{
			ServiceShutdown:      Duration(30 * time.Second),
			ProviderReload:       Duration(30 * time.Second),
			GracefulShutdown:     Duration(15 * time.Second),
			HealthCheckInterval:  Duration(2 * time.Second),
			ConfigReloadInterval: Duration(2 * time.Second),
			ConfigReloadDebounce: Duration(500 * time.Millisecond),
		},
	}
}

// withDefaults returns a copy of cfg with any zero Timeout fields filled from
// the built-in defaults. It is used during unmarshaling/migration.
func (c *Config) withTimeoutsDefaults() {
	def := DefaultTimeouts()
	mergeTimeouts(&c.Timeouts, &def)
}

func mergeTimeouts(dst, src *TimeoutsConfig) {
	mergeDuration(&dst.LLM.RequestSeconds, src.LLM.RequestSeconds)
	mergeDuration(&dst.LLM.StreamingIdle, src.LLM.StreamingIdle)
	mergeDuration(&dst.LLM.ProviderInit, src.LLM.ProviderInit)
	mergeDuration(&dst.LLM.FirstTokenTimeout, src.LLM.FirstTokenTimeout)
	mergeDuration(&dst.LLM.CredentialCacheTTL, src.LLM.CredentialCacheTTL)
	mergeDuration(&dst.LLM.CooldownFailureWindow, src.LLM.CooldownFailureWindow)

	mergeDuration(&dst.Tools.ExecSeconds, src.Tools.ExecSeconds)
	mergeDuration(&dst.Tools.CronExecMinutes, src.Tools.CronExecMinutes)
	mergeDuration(&dst.Tools.WebSearchSeconds, src.Tools.WebSearchSeconds)
	mergeDuration(&dst.Tools.WebPerplexitySeconds, src.Tools.WebPerplexitySeconds)
	mergeDuration(&dst.Tools.WebFetchSeconds, src.Tools.WebFetchSeconds)
	mergeDuration(&dst.Tools.SessionCleanupInterval, src.Tools.SessionCleanupInterval)
	mergeDuration(&dst.Tools.SessionCleanupAge, src.Tools.SessionCleanupAge)
	mergeDuration(&dst.Tools.ProcessReapGrace, src.Tools.ProcessReapGrace)
	mergeDuration(&dst.Tools.SerialPollInterval, src.Tools.SerialPollInterval)

	mergeDuration(&dst.Agent.SubTurnDefaultMinutes, src.Agent.SubTurnDefaultMinutes)
	mergeDuration(&dst.Agent.SubTurnConcurrencySeconds, src.Agent.SubTurnConcurrencySeconds)
	mergeDuration(&dst.Agent.ProviderReloadGrace, src.Agent.ProviderReloadGrace)
	mergeDuration(&dst.Agent.IdleLoopInterval, src.Agent.IdleLoopInterval)

	mergeDuration(&dst.Mesh.RemoteCall, src.Mesh.RemoteCall)
	mergeDuration(&dst.Mesh.CapabilityAdvertise, src.Mesh.CapabilityAdvertise)
	mergeDuration(&dst.Mesh.ProtocolNegotiation, src.Mesh.ProtocolNegotiation)

	mergeInt(&dst.Network.BootstrapAttempts, src.Network.BootstrapAttempts)
	mergeDuration(&dst.Network.ListenerReady, src.Network.ListenerReady)
	mergeDuration(&dst.Network.BootstrapBackoff, src.Network.BootstrapBackoff)
	mergeDuration(&dst.Network.Ping, src.Network.Ping)
	mergeDuration(&dst.Network.DHTDial, src.Network.DHTDial)
	mergeDuration(&dst.Network.DHTQuery, src.Network.DHTQuery)
	mergeDuration(&dst.Network.DHTRetry, src.Network.DHTRetry)
	mergeDuration(&dst.Network.DHTReprovideInterval, src.Network.DHTReprovideInterval)

	mergeDuration(&dst.Sync.CommitInterval, src.Sync.CommitInterval)
	mergeDuration(&dst.Sync.AnnounceInterval, src.Sync.AnnounceInterval)
	mergeDuration(&dst.Sync.PeerWait, src.Sync.PeerWait)
	mergeDuration(&dst.Sync.FetchRetry, src.Sync.FetchRetry)
	mergeDuration(&dst.Sync.FetchAttemptTimeout, src.Sync.FetchAttemptTimeout)
	mergeDuration(&dst.Sync.TransportRequest, src.Sync.TransportRequest)
	mergeDuration(&dst.Sync.TransportPackfile, src.Sync.TransportPackfile)
	mergeDuration(&dst.Sync.WatcherDebounce, src.Sync.WatcherDebounce)

	mergeDuration(&dst.HTTP.Request, src.HTTP.Request)
	mergeDuration(&dst.HTTP.IdleConn, src.HTTP.IdleConn)
	mergeDuration(&dst.HTTP.TLSHandshake, src.HTTP.TLSHandshake)
	mergeDuration(&dst.HTTP.Dial, src.HTTP.Dial)
	mergeDuration(&dst.HTTP.KeepAlive, src.HTTP.KeepAlive)
	mergeDuration(&dst.HTTP.RetryDelayUnit, src.HTTP.RetryDelayUnit)
	mergeDuration(&dst.HTTP.MaxRetrySleep, src.HTTP.MaxRetrySleep)
	mergeInt(&dst.HTTP.MaxRetries, src.HTTP.MaxRetries)
	mergeInt(&dst.HTTP.MaxRedirects, src.HTTP.MaxRedirects)

	mergeDuration(&dst.Media.Download, src.Media.Download)
	mergeDuration(&dst.Media.MaxAge, src.Media.MaxAge)
	mergeDuration(&dst.Media.CleanupInterval, src.Media.CleanupInterval)

	mergeDuration(&dst.Gateway.ServiceShutdown, src.Gateway.ServiceShutdown)
	mergeDuration(&dst.Gateway.ProviderReload, src.Gateway.ProviderReload)
	mergeDuration(&dst.Gateway.GracefulShutdown, src.Gateway.GracefulShutdown)
	mergeDuration(&dst.Gateway.HealthCheckInterval, src.Gateway.HealthCheckInterval)

	mergeDuration(&dst.Cron.NextWakeResolution, src.Cron.NextWakeResolution)
	mergeDuration(&dst.Cron.HeartbeatInitial, src.Cron.HeartbeatInitial)

	mergeDuration(&dst.Evolution.TaskSuccessJudge, src.Evolution.TaskSuccessJudge)
	mergeDuration(&dst.Evolution.PatternCluster, src.Evolution.PatternCluster)
	mergeDuration(&dst.Evolution.DraftGeneration, src.Evolution.DraftGeneration)
	mergeDuration(&dst.Evolution.SkillCold, src.Evolution.SkillCold)
	mergeDuration(&dst.Evolution.SkillArchived, src.Evolution.SkillArchived)
	mergeDuration(&dst.Evolution.SkillDeleted, src.Evolution.SkillDeleted)

	mergeDuration(&dst.Health.Read, src.Health.Read)
	mergeDuration(&dst.Health.Write, src.Health.Write)

	mergeDuration(&dst.Heartbeat.Interval, src.Heartbeat.Interval)
	mergeDuration(&dst.Heartbeat.InitialDelay, src.Heartbeat.InitialDelay)
	mergeDuration(&dst.Heartbeat.PublishTimeout, src.Heartbeat.PublishTimeout)

	mergeDuration(&dst.Updater.Download, src.Updater.Download)
	mergeDuration(&dst.Updater.ProgressTick, src.Updater.ProgressTick)

	mergeDuration(&dst.Channel.RequestTimeout, src.Channel.RequestTimeout)
	mergeDuration(&dst.Channel.ConnectTimeout, src.Channel.ConnectTimeout)
	mergeDuration(&dst.Channel.CommandTimeout, src.Channel.CommandTimeout)
	mergeDuration(&dst.Channel.AuthTimeout, src.Channel.AuthTimeout)
	mergeDuration(&dst.Channel.MediaTimeout, src.Channel.MediaTimeout)
	mergeDuration(&dst.Channel.PublishTimeout, src.Channel.PublishTimeout)
	mergeDuration(&dst.Channel.HeartbeatInterval, src.Channel.HeartbeatInterval)
	mergeDuration(&dst.Channel.ReconnectInitial, src.Channel.ReconnectInitial)
	mergeDuration(&dst.Channel.ReconnectMax, src.Channel.ReconnectMax)
	mergeDuration(&dst.Channel.MessageCacheTTL, src.Channel.MessageCacheTTL)
	mergeDuration(&dst.Channel.ConfigCacheTTL, src.Channel.ConfigCacheTTL)
	mergeDuration(&dst.Channel.SessionPauseDuration, src.Channel.SessionPauseDuration)
	mergeDuration(&dst.Channel.MediaGroupDelay, src.Channel.MediaGroupDelay)
	mergeDuration(&dst.Channel.TypingMaxDuration, src.Channel.TypingMaxDuration)
	mergeDuration(&dst.Channel.ToolFeedbackInterval, src.Channel.ToolFeedbackInterval)
	mergeDuration(&dst.Channel.StreamMaxDuration, src.Channel.StreamMaxDuration)
	mergeDuration(&dst.Channel.StreamMinInterval, src.Channel.StreamMinInterval)
	mergeDuration(&dst.Channel.RouteTTL, src.Channel.RouteTTL)
	mergeDuration(&dst.Channel.PollInterval, src.Channel.PollInterval)
	mergeDuration(&dst.Channel.RateLimitDelay, src.Channel.RateLimitDelay)
	mergeDuration(&dst.Channel.MaxBackoff, src.Channel.MaxBackoff)
	mergeDuration(&dst.Channel.JanitorInterval, src.Channel.JanitorInterval)
	mergeDuration(&dst.Channel.PlaceholderTTL, src.Channel.PlaceholderTTL)
	mergeDuration(&dst.Channel.TypingRefreshInterval, src.Channel.TypingRefreshInterval)

	mergeDuration(&dst.Gateway.ConfigReloadInterval, src.Gateway.ConfigReloadInterval)
	mergeDuration(&dst.Gateway.ConfigReloadDebounce, src.Gateway.ConfigReloadDebounce)
}

func mergeDuration(dst *Duration, src Duration) {
	if *dst == 0 {
		*dst = src
	}
}

func mergeInt(dst *int, src int) {
	if *dst == 0 {
		*dst = src
	}
}

// --- Resolvers ---

// LLMRequestSeconds returns the effective LLM request timeout for a model,
// falling back through per-model request_timeout, global timeouts, and the
// package default.
func (c *Config) LLMRequestSeconds(modelCfg *ModelConfig) time.Duration {
	if modelCfg != nil && modelCfg.RequestTimeout > 0 {
		return time.Duration(modelCfg.RequestTimeout) * time.Second
	}
	if c.Timeouts.LLM.RequestSeconds > 0 {
		return c.Timeouts.LLM.RequestSeconds.Duration()
	}
	return providercommon.DefaultRequestTimeout
}

// LLMStreamingIdle returns the max idle time between streaming tokens.
func (c *Config) LLMStreamingIdle() time.Duration {
	if c.Timeouts.LLM.StreamingIdle > 0 {
		return c.Timeouts.LLM.StreamingIdle.Duration()
	}
	return 5 * time.Minute
}

// LLMProviderInit returns the timeout for provider credential/region init.
func (c *Config) LLMProviderInit() time.Duration {
	if c.Timeouts.LLM.ProviderInit > 0 {
		return c.Timeouts.LLM.ProviderInit.Duration()
	}
	return 30 * time.Second
}

// LLMProviderCredentialCacheTTL returns how long external CLI provider
// credentials are considered valid before re-reading.
func (c *Config) LLMProviderCredentialCacheTTL() time.Duration {
	if c.Timeouts.LLM.CredentialCacheTTL > 0 {
		return c.Timeouts.LLM.CredentialCacheTTL.Duration()
	}
	return time.Hour
}

// LLMCooldownFailureWindow returns the time window used for provider fallback
// cooldown tracking.
func (c *Config) LLMCooldownFailureWindow() time.Duration {
	if c.Timeouts.LLM.CooldownFailureWindow > 0 {
		return c.Timeouts.LLM.CooldownFailureWindow.Duration()
	}
	return 24 * time.Hour
}

// ToolExecTimeout returns the effective shell exec timeout.
func (c *Config) ToolExecTimeout() time.Duration {
	if c.Tools.Exec.TimeoutSeconds > 0 {
		return time.Duration(c.Tools.Exec.TimeoutSeconds) * time.Second
	}
	if c.Timeouts.Tools.ExecSeconds > 0 {
		return c.Timeouts.Tools.ExecSeconds.Duration()
	}
	return 60 * time.Second
}

// ToolCronExecTimeout returns the cron job exec timeout.
func (c *Config) ToolCronExecTimeout() time.Duration {
	if c.Tools.Cron.ExecTimeoutMinutes > 0 {
		return time.Duration(c.Tools.Cron.ExecTimeoutMinutes) * time.Minute
	}
	if c.Timeouts.Tools.CronExecMinutes > 0 {
		return c.Timeouts.Tools.CronExecMinutes.Duration()
	}
	return 5 * time.Minute
}

// SubTurnDefaultTimeout returns the default subturn timeout.
func (c *Config) SubTurnDefaultTimeout() time.Duration {
	if c.Agents.Defaults.SubTurn.DefaultTimeoutMinutes > 0 {
		return time.Duration(c.Agents.Defaults.SubTurn.DefaultTimeoutMinutes) * time.Minute
	}
	if c.Timeouts.Agent.SubTurnDefaultMinutes > 0 {
		return c.Timeouts.Agent.SubTurnDefaultMinutes.Duration()
	}
	return 5 * time.Minute
}

// SubTurnConcurrencyTimeout returns the subturn concurrency timeout.
func (c *Config) SubTurnConcurrencyTimeout() time.Duration {
	if c.Agents.Defaults.SubTurn.ConcurrencyTimeoutSec > 0 {
		return time.Duration(c.Agents.Defaults.SubTurn.ConcurrencyTimeoutSec) * time.Second
	}
	if c.Timeouts.Agent.SubTurnConcurrencySeconds > 0 {
		return c.Timeouts.Agent.SubTurnConcurrencySeconds.Duration()
	}
	return 30 * time.Second
}

// MeshRemoteTimeout returns the mesh remote call timeout.
func (c *Config) MeshRemoteTimeout() time.Duration {
	if c.Mesh.RemoteTimeout > 0 {
		return c.Mesh.RemoteTimeout
	}
	if c.Timeouts.Mesh.RemoteCall > 0 {
		return c.Timeouts.Mesh.RemoteCall.Duration()
	}
	return 5 * time.Minute
}

// MeshCapabilityAdvertiseInterval returns the interval between capability ads.
func (c *Config) MeshCapabilityAdvertiseInterval() time.Duration {
	if c.Timeouts.Mesh.CapabilityAdvertise > 0 {
		return c.Timeouts.Mesh.CapabilityAdvertise.Duration()
	}
	return 5 * time.Minute
}

// MeshProtocolNegotiationTimeout returns how long to wait for a peer to support
// an agentrpc/sync protocol.
func (c *Config) MeshProtocolNegotiationTimeout() time.Duration {
	if c.Timeouts.Mesh.ProtocolNegotiation > 0 {
		return c.Timeouts.Mesh.ProtocolNegotiation.Duration()
	}
	return 5 * time.Second
}

// AgentIdleLoopInterval returns the main agent loop idle ticker interval.
func (c *Config) AgentIdleLoopInterval() time.Duration {
	if c.Timeouts.Agent.IdleLoopInterval > 0 {
		return c.Timeouts.Agent.IdleLoopInterval.Duration()
	}
	return 100 * time.Millisecond
}

// AgentProviderReloadGrace returns the grace period for in-flight requests to
// drain before a provider reload completes.
func (c *Config) AgentProviderReloadGrace() time.Duration {
	if c.Timeouts.Agent.ProviderReloadGrace > 0 {
		return c.Timeouts.Agent.ProviderReloadGrace.Duration()
	}
	return 30 * time.Second
}

// ToolProcessReapGrace returns the grace period after a tool timeout before the
// process is forcibly killed.
func (c *Config) ToolProcessReapGrace() time.Duration {
	if c.Timeouts.Tools.ProcessReapGrace > 0 {
		return c.Timeouts.Tools.ProcessReapGrace.Duration()
	}
	return 2 * time.Second
}

// ToolSessionCleanupInterval returns how often tool sessions are scanned for
// cleanup.
func (c *Config) ToolSessionCleanupInterval() time.Duration {
	if c.Timeouts.Tools.SessionCleanupInterval > 0 {
		return c.Timeouts.Tools.SessionCleanupInterval.Duration()
	}
	return 5 * time.Minute
}

// ToolSessionCleanupAge returns how long a tool session can sit idle before it
// is eligible for cleanup.
func (c *Config) ToolSessionCleanupAge() time.Duration {
	if c.Timeouts.Tools.SessionCleanupAge > 0 {
		return c.Timeouts.Tools.SessionCleanupAge.Duration()
	}
	return 30 * time.Minute
}

// ToolWebFetchTimeout returns the timeout for a single web fetch.
func (c *Config) ToolWebFetchTimeout() time.Duration {
	if c.Timeouts.Tools.WebFetchSeconds > 0 {
		return c.Timeouts.Tools.WebFetchSeconds.Duration()
	}
	return 60 * time.Second
}

// ToolWebSearchTimeout returns the timeout for a web search query.
func (c *Config) ToolWebSearchTimeout() time.Duration {
	if c.Timeouts.Tools.WebSearchSeconds > 0 {
		return c.Timeouts.Tools.WebSearchSeconds.Duration()
	}
	return 10 * time.Second
}

// ToolWebPerplexityTimeout returns the timeout for a Perplexity web search.
func (c *Config) ToolWebPerplexityTimeout() time.Duration {
	if c.Timeouts.Tools.WebPerplexitySeconds > 0 {
		return c.Timeouts.Tools.WebPerplexitySeconds.Duration()
	}
	return 30 * time.Second
}

// ToolSerialPollInterval returns the poll interval for serial hardware tools.
func (c *Config) ToolSerialPollInterval() time.Duration {
	if c.Timeouts.Tools.SerialPollInterval > 0 {
		return c.Timeouts.Tools.SerialPollInterval.Duration()
	}
	return 100 * time.Millisecond
}

// HTTPClient returns an http.Client configured from the timeouts config.
func (c *Config) HTTPClient() *http.Client {
	return &http.Client{
		Timeout:   c.HTTPRequestTimeout(),
		Transport: c.HTTPTransport(),
	}
}

// HTTPRequestTimeout returns the overall HTTP request timeout.
func (c *Config) HTTPRequestTimeout() time.Duration {
	if c.Timeouts.HTTP.Request > 0 {
		return c.Timeouts.HTTP.Request.Duration()
	}
	return 120 * time.Second
}

// HTTPTransport returns a default transport with configured dial/TLS/idle
// timeouts.
func (c *Config) HTTPTransport() *http.Transport {
	dial := c.HTTPDialTimeout()
	if dial <= 0 {
		dial = 15 * time.Second
	}
	tls := c.HTTPTLSHandshakeTimeout()
	if tls <= 0 {
		tls = 15 * time.Second
	}
	idle := c.HTTPIdleConnTimeout()
	if idle <= 0 {
		idle = 30 * time.Second
	}
	keepalive := c.HTTPKeepAlive()
	if keepalive <= 0 {
		keepalive = 30 * time.Second
	}
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dial,
			KeepAlive: keepalive,
		}).DialContext,
		TLSHandshakeTimeout: tls,
		IdleConnTimeout:     idle,
	}
}

// HTTPDialTimeout returns the HTTP dialer timeout.
func (c *Config) HTTPDialTimeout() time.Duration {
	if c.Timeouts.HTTP.Dial > 0 {
		return c.Timeouts.HTTP.Dial.Duration()
	}
	return 15 * time.Second
}

// HTTPTLSHandshakeTimeout returns the HTTP TLS handshake timeout.
func (c *Config) HTTPTLSHandshakeTimeout() time.Duration {
	if c.Timeouts.HTTP.TLSHandshake > 0 {
		return c.Timeouts.HTTP.TLSHandshake.Duration()
	}
	return 15 * time.Second
}

// HTTPIdleConnTimeout returns the idle connection timeout for the HTTP
// transport.
func (c *Config) HTTPIdleConnTimeout() time.Duration {
	if c.Timeouts.HTTP.IdleConn > 0 {
		return c.Timeouts.HTTP.IdleConn.Duration()
	}
	return 30 * time.Second
}

// HTTPKeepAlive returns the HTTP keep-alive period.
func (c *Config) HTTPKeepAlive() time.Duration {
	if c.Timeouts.HTTP.KeepAlive > 0 {
		return c.Timeouts.HTTP.KeepAlive.Duration()
	}
	return 30 * time.Second
}

// HTTPRetryDelay returns the unit retry delay for HTTP clients.
func (c *Config) HTTPRetryDelay() time.Duration {
	if c.Timeouts.HTTP.RetryDelayUnit > 0 {
		return c.Timeouts.HTTP.RetryDelayUnit.Duration()
	}
	return time.Second
}

// HTTPMaxRetrySleep returns the maximum sleep between HTTP retries.
func (c *Config) HTTPMaxRetrySleep() time.Duration {
	if c.Timeouts.HTTP.MaxRetrySleep > 0 {
		return c.Timeouts.HTTP.MaxRetrySleep.Duration()
	}
	return time.Minute
}

// HTTPMaxRetries returns the default maximum HTTP retry count.
func (c *Config) HTTPMaxRetries() int {
	if c.Timeouts.HTTP.MaxRetries > 0 {
		return c.Timeouts.HTTP.MaxRetries
	}
	return 3
}

// HTTPMaxRedirects returns the default maximum HTTP redirect count.
func (c *Config) HTTPMaxRedirects() int {
	if c.Timeouts.HTTP.MaxRedirects > 0 {
		return c.Timeouts.HTTP.MaxRedirects
	}
	return 10
}

// MediaDownloadTimeout returns the timeout for media downloads.
func (c *Config) MediaDownloadTimeout() time.Duration {
	if c.Timeouts.Media.Download > 0 {
		return c.Timeouts.Media.Download.Duration()
	}
	return 60 * time.Second
}

// MediaMaxAge returns the maximum age before media is eligible for cleanup.
func (c *Config) MediaMaxAge() time.Duration {
	if c.Tools.MediaCleanup.MaxAge > 0 {
		return time.Duration(c.Tools.MediaCleanup.MaxAge) * time.Minute
	}
	if c.Timeouts.Media.MaxAge > 0 {
		return c.Timeouts.Media.MaxAge.Duration()
	}
	return 15 * time.Minute
}

// MediaCleanupInterval returns how often the media cleanup task runs.
func (c *Config) MediaCleanupInterval() time.Duration {
	if c.Tools.MediaCleanup.Interval > 0 {
		return time.Duration(c.Tools.MediaCleanup.Interval) * time.Minute
	}
	if c.Timeouts.Media.CleanupInterval > 0 {
		return c.Timeouts.Media.CleanupInterval.Duration()
	}
	return 5 * time.Minute
}

// GatewayServiceShutdown returns the timeout for shutting down a gateway
// service.
func (c *Config) GatewayServiceShutdown() time.Duration {
	if c.Timeouts.Gateway.ServiceShutdown > 0 {
		return c.Timeouts.Gateway.ServiceShutdown.Duration()
	}
	return 30 * time.Second
}

// GatewayProviderReload returns the timeout for a gateway provider reload.
func (c *Config) GatewayProviderReload() time.Duration {
	if c.Timeouts.Gateway.ProviderReload > 0 {
		return c.Timeouts.Gateway.ProviderReload.Duration()
	}
	return 30 * time.Second
}

// GatewayGracefulShutdown returns the graceful shutdown timeout.
func (c *Config) GatewayGracefulShutdown() time.Duration {
	if c.Timeouts.Gateway.GracefulShutdown > 0 {
		return c.Timeouts.Gateway.GracefulShutdown.Duration()
	}
	return 15 * time.Second
}

// GatewayHealthCheckInterval returns the interval between gateway health checks.
func (c *Config) GatewayHealthCheckInterval() time.Duration {
	if c.Timeouts.Gateway.HealthCheckInterval > 0 {
		return c.Timeouts.Gateway.HealthCheckInterval.Duration()
	}
	return 2 * time.Second
}

// CronNextWakeResolution returns the cron scheduler wake resolution.
func (c *Config) CronNextWakeResolution() time.Duration {
	if c.Timeouts.Cron.NextWakeResolution > 0 {
		return c.Timeouts.Cron.NextWakeResolution.Duration()
	}
	return time.Hour
}

// CronHeartbeatInitial returns the initial cron heartbeat delay.
func (c *Config) CronHeartbeatInitial() time.Duration {
	if c.Timeouts.Cron.HeartbeatInitial > 0 {
		return c.Timeouts.Cron.HeartbeatInitial.Duration()
	}
	return time.Second
}

// EvolutionTaskSuccessJudge returns the LLM timeout for task success judging.
func (c *Config) EvolutionTaskSuccessJudge() time.Duration {
	if c.Timeouts.Evolution.TaskSuccessJudge > 0 {
		return c.Timeouts.Evolution.TaskSuccessJudge.Duration()
	}
	return 15 * time.Second
}

// EvolutionPatternCluster returns the LLM timeout for pattern clustering.
func (c *Config) EvolutionPatternCluster() time.Duration {
	if c.Timeouts.Evolution.PatternCluster > 0 {
		return c.Timeouts.Evolution.PatternCluster.Duration()
	}
	return 45 * time.Second
}

// EvolutionDraftGeneration returns the LLM timeout for draft generation.
func (c *Config) EvolutionDraftGeneration() time.Duration {
	if c.Timeouts.Evolution.DraftGeneration > 0 {
		return c.Timeouts.Evolution.DraftGeneration.Duration()
	}
	return 60 * time.Second
}

// EvolutionSkillColdThreshold returns the idle time before an active skill
// becomes cold.
func (c *Config) EvolutionSkillColdThreshold() time.Duration {
	if c.Timeouts.Evolution.SkillCold > 0 {
		return c.Timeouts.Evolution.SkillCold.Duration()
	}
	return 90 * 24 * time.Hour
}

// EvolutionSkillArchivedThreshold returns the idle time before a cold skill
// becomes archived.
func (c *Config) EvolutionSkillArchivedThreshold() time.Duration {
	if c.Timeouts.Evolution.SkillArchived > 0 {
		return c.Timeouts.Evolution.SkillArchived.Duration()
	}
	return 180 * 24 * time.Hour
}

// EvolutionSkillDeletedThreshold returns the idle time before an archived skill
// is deleted.
func (c *Config) EvolutionSkillDeletedThreshold() time.Duration {
	if c.Timeouts.Evolution.SkillDeleted > 0 {
		return c.Timeouts.Evolution.SkillDeleted.Duration()
	}
	return 365 * 24 * time.Hour
}

// HealthReadTimeout returns the health server read timeout.
func (c *Config) HealthReadTimeout() time.Duration {
	if c.Timeouts.Health.Read > 0 {
		return c.Timeouts.Health.Read.Duration()
	}
	return 5 * time.Second
}

// HealthWriteTimeout returns the health server write timeout.
func (c *Config) HealthWriteTimeout() time.Duration {
	if c.Timeouts.Health.Write > 0 {
		return c.Timeouts.Health.Write.Duration()
	}
	return 5 * time.Second
}

// HeartbeatInterval returns the interval between heartbeats.
func (c *Config) HeartbeatInterval() time.Duration {
	if c.Heartbeat.Interval > 0 {
		return time.Duration(c.Heartbeat.Interval) * time.Minute
	}
	if c.Timeouts.Heartbeat.Interval > 0 {
		return c.Timeouts.Heartbeat.Interval.Duration()
	}
	return 30 * time.Minute
}

// HeartbeatInitialDelay returns the delay before the first heartbeat.
func (c *Config) HeartbeatInitialDelay() time.Duration {
	if c.Timeouts.Heartbeat.InitialDelay > 0 {
		return c.Timeouts.Heartbeat.InitialDelay.Duration()
	}
	return time.Second
}

// HeartbeatPublishTimeout returns the timeout for a heartbeat publish.
func (c *Config) HeartbeatPublishTimeout() time.Duration {
	if c.Timeouts.Heartbeat.PublishTimeout > 0 {
		return c.Timeouts.Heartbeat.PublishTimeout.Duration()
	}
	return 5 * time.Second
}

// UpdaterDownloadTimeout returns the timeout for update downloads.
func (c *Config) UpdaterDownloadTimeout() time.Duration {
	if c.Timeouts.Updater.Download > 0 {
		return c.Timeouts.Updater.Download.Duration()
	}
	return 2 * time.Minute
}

// UpdaterProgressTick returns the throttle interval for updater progress.
func (c *Config) UpdaterProgressTick() time.Duration {
	if c.Timeouts.Updater.ProgressTick > 0 {
		return c.Timeouts.Updater.ProgressTick.Duration()
	}
	return 200 * time.Millisecond
}

// GatewayConfigReloadInterval returns the polling interval for config hot reload.
func (c *Config) GatewayConfigReloadInterval() time.Duration {
	if c.Timeouts.Gateway.ConfigReloadInterval > 0 {
		return c.Timeouts.Gateway.ConfigReloadInterval.Duration()
	}
	return 2 * time.Second
}

// GatewayConfigReloadDebounce returns the debounce wait after a config change
// is detected.
func (c *Config) GatewayConfigReloadDebounce() time.Duration {
	if c.Timeouts.Gateway.ConfigReloadDebounce > 0 {
		return c.Timeouts.Gateway.ConfigReloadDebounce.Duration()
	}
	return 500 * time.Millisecond
}

// ChannelRequestTimeout returns the generic channel request timeout.
func (c *Config) ChannelRequestTimeout() time.Duration {
	if c.Timeouts.Channel.RequestTimeout > 0 {
		return c.Timeouts.Channel.RequestTimeout.Duration()
	}
	return 30 * time.Second
}

// ChannelConnectTimeout returns the channel connection timeout.
func (c *Config) ChannelConnectTimeout() time.Duration {
	if c.Timeouts.Channel.ConnectTimeout > 0 {
		return c.Timeouts.Channel.ConnectTimeout.Duration()
	}
	return 15 * time.Second
}

// ChannelCommandTimeout returns the channel command timeout.
func (c *Config) ChannelCommandTimeout() time.Duration {
	if c.Timeouts.Channel.CommandTimeout > 0 {
		return c.Timeouts.Channel.CommandTimeout.Duration()
	}
	return 10 * time.Second
}

// ChannelAuthTimeout returns the channel authentication timeout.
func (c *Config) ChannelAuthTimeout() time.Duration {
	if c.Timeouts.Channel.AuthTimeout > 0 {
		return c.Timeouts.Channel.AuthTimeout.Duration()
	}
	return 5 * time.Minute
}

// ChannelMediaTimeout returns the channel media upload/download timeout.
func (c *Config) ChannelMediaTimeout() time.Duration {
	if c.Timeouts.Channel.MediaTimeout > 0 {
		return c.Timeouts.Channel.MediaTimeout.Duration()
	}
	return 30 * time.Second
}

// ChannelPublishTimeout returns the timeout for publishing a channel message.
func (c *Config) ChannelPublishTimeout() time.Duration {
	if c.Timeouts.Channel.PublishTimeout > 0 {
		return c.Timeouts.Channel.PublishTimeout.Duration()
	}
	return 5 * time.Second
}

// ChannelHeartbeatInterval returns the interval between channel heartbeats.
func (c *Config) ChannelHeartbeatInterval() time.Duration {
	if c.Timeouts.Channel.HeartbeatInterval > 0 {
		return c.Timeouts.Channel.HeartbeatInterval.Duration()
	}
	return 30 * time.Second
}

// ChannelReconnectInitial returns the initial reconnect backoff.
func (c *Config) ChannelReconnectInitial() time.Duration {
	if c.Timeouts.Channel.ReconnectInitial > 0 {
		return c.Timeouts.Channel.ReconnectInitial.Duration()
	}
	return 5 * time.Second
}

// ChannelReconnectMax returns the maximum reconnect backoff.
func (c *Config) ChannelReconnectMax() time.Duration {
	if c.Timeouts.Channel.ReconnectMax > 0 {
		return c.Timeouts.Channel.ReconnectMax.Duration()
	}
	return 5 * time.Minute
}

// ChannelMessageCacheTTL returns the TTL for channel message caches.
func (c *Config) ChannelMessageCacheTTL() time.Duration {
	if c.Timeouts.Channel.MessageCacheTTL > 0 {
		return c.Timeouts.Channel.MessageCacheTTL.Duration()
	}
	return 30 * time.Second
}

// ChannelConfigCacheTTL returns the TTL for channel configuration caches.
func (c *Config) ChannelConfigCacheTTL() time.Duration {
	if c.Timeouts.Channel.ConfigCacheTTL > 0 {
		return c.Timeouts.Channel.ConfigCacheTTL.Duration()
	}
	return 24 * time.Hour
}

// ChannelSessionPauseDuration returns how long a channel session remains paused.
func (c *Config) ChannelSessionPauseDuration() time.Duration {
	if c.Timeouts.Channel.SessionPauseDuration > 0 {
		return c.Timeouts.Channel.SessionPauseDuration.Duration()
	}
	return time.Hour
}

// ChannelMediaGroupDelay returns the delay for grouping channel media messages.
func (c *Config) ChannelMediaGroupDelay() time.Duration {
	if c.Timeouts.Channel.MediaGroupDelay > 0 {
		return c.Timeouts.Channel.MediaGroupDelay.Duration()
	}
	return 500 * time.Millisecond
}

// ChannelTypingMaxDuration returns the maximum duration for sending typing
// indicators.
func (c *Config) ChannelTypingMaxDuration() time.Duration {
	if c.Timeouts.Channel.TypingMaxDuration > 0 {
		return c.Timeouts.Channel.TypingMaxDuration.Duration()
	}
	return 5 * time.Minute
}

// ChannelToolFeedbackInterval returns the interval between tool feedback
// animation frames.
func (c *Config) ChannelToolFeedbackInterval() time.Duration {
	if c.Timeouts.Channel.ToolFeedbackInterval > 0 {
		return c.Timeouts.Channel.ToolFeedbackInterval.Duration()
	}
	return 3 * time.Second
}

// ChannelStreamMaxDuration returns the maximum channel streaming session
// duration.
func (c *Config) ChannelStreamMaxDuration() time.Duration {
	if c.Timeouts.Channel.StreamMaxDuration > 0 {
		return c.Timeouts.Channel.StreamMaxDuration.Duration()
	}
	return 5*time.Minute + 30*time.Second
}

// ChannelStreamMinInterval returns the minimum interval between channel stream
// chunks.
func (c *Config) ChannelStreamMinInterval() time.Duration {
	if c.Timeouts.Channel.StreamMinInterval > 0 {
		return c.Timeouts.Channel.StreamMinInterval.Duration()
	}
	return 500 * time.Millisecond
}

// ChannelRouteTTL returns the TTL for channel route caches.
func (c *Config) ChannelRouteTTL() time.Duration {
	if c.Timeouts.Channel.RouteTTL > 0 {
		return c.Timeouts.Channel.RouteTTL.Duration()
	}
	return 30 * time.Minute
}

// ChannelPollInterval returns the polling interval for channels.
func (c *Config) ChannelPollInterval() time.Duration {
	if c.Timeouts.Channel.PollInterval > 0 {
		return c.Timeouts.Channel.PollInterval.Duration()
	}
	return 2 * time.Second
}

// ChannelRateLimitDelay returns the delay applied after a channel rate-limit
// failure before retrying.
func (c *Config) ChannelRateLimitDelay() time.Duration {
	if c.Timeouts.Channel.RateLimitDelay > 0 {
		return c.Timeouts.Channel.RateLimitDelay.Duration()
	}
	return 1 * time.Second
}

// ChannelMaxBackoff returns the maximum exponential backoff for channel
// retries.
func (c *Config) ChannelMaxBackoff() time.Duration {
	if c.Timeouts.Channel.MaxBackoff > 0 {
		return c.Timeouts.Channel.MaxBackoff.Duration()
	}
	return 8 * time.Second
}

// ChannelJanitorInterval returns how often the channel manager TTL janitor
// wakes to clean up typing/placeholder/reaction entries.
func (c *Config) ChannelJanitorInterval() time.Duration {
	if c.Timeouts.Channel.JanitorInterval > 0 {
		return c.Timeouts.Channel.JanitorInterval.Duration()
	}
	return 10 * time.Second
}

// ChannelPlaceholderTTL returns how long message placeholder metadata is kept.
func (c *Config) ChannelPlaceholderTTL() time.Duration {
	if c.Timeouts.Channel.PlaceholderTTL > 0 {
		return c.Timeouts.Channel.PlaceholderTTL.Duration()
	}
	return 10 * time.Minute
}

// ChannelTypingRefreshInterval returns how often typing indicators are
// refreshed while a long-running task is in progress.
func (c *Config) ChannelTypingRefreshInterval() time.Duration {
	if c.Timeouts.Channel.TypingRefreshInterval > 0 {
		return c.Timeouts.Channel.TypingRefreshInterval.Duration()
	}
	return 20 * time.Second
}

// ToolSkillsSearchCacheTTL returns the skill search cache TTL.
func (c *Config) ToolSkillsSearchCacheTTL() time.Duration {
	if c.Tools.Skills.SearchCache.TTLSeconds > 0 {
		return time.Duration(c.Tools.Skills.SearchCache.TTLSeconds) * time.Second
	}
	return 5 * time.Minute
}
