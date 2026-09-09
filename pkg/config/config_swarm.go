package config

import (
	"encoding/json"
	"time"
)

const (
	// DefaultSwarmMaxMembers bounds a swarm's member roster so a broadcast
	// stays cheap on the direct-stream transport.
	DefaultSwarmMaxMembers = 32
	// DefaultSwarmMaxMessageBytes caps a single swarm envelope payload.
	DefaultSwarmMaxMessageBytes = 256 * 1024
	// DefaultSwarmRequestMaxSkew is the accepted clock difference for signed
	// swarm envelopes when swarm.request_max_skew is unset.
	DefaultSwarmRequestMaxSkew = 2 * time.Minute
)

// SwarmConfig controls swarm (named trusted-peer group) collaboration.
// Swarms are always scoped to the existing mesh trust set: a peer must be
// trusted before it can join or be seen as a swarm member.
type SwarmConfig struct {
	// Enabled turns the swarm layer on. Requires mesh.enabled.
	Enabled bool `json:"enabled,omitempty"`
	// Memberships lists swarm ids this node joins automatically on start.
	Memberships []string `json:"memberships,omitempty"`
	// MaxMembers bounds each swarm's member roster. Defaults to 32.
	MaxMembers int `json:"max_members,omitempty"`
	// MaxMessageBytes caps a single swarm envelope payload. Defaults to
	// 256 KiB.
	MaxMessageBytes int `json:"max_message_bytes,omitempty"`
	// RequestMaxSkew is the maximum accepted clock difference between swarm
	// peers for signed envelope timestamps. Defaults to 2m.
	RequestMaxSkew time.Duration `json:"request_max_skew,omitempty"`
	// Transport selects the swarm broadcast backend: "direct" (default,
	// per-member stream fan-out) or "gossipsub" (pub/sub topics; optional).
	Transport string `json:"transport,omitempty"`
	// Presence tunes the heartbeat/liveness tracker.
	Presence SwarmPresenceConfig `json:"presence,omitempty"`
	// Queue tunes the distributed offer/claim work queue.
	Queue SwarmQueueConfig `json:"queue,omitempty"`

	// RateLimitPerPeer caps inbound swarm envelopes per peer, in messages per
	// minute. 0 (or unset) defaults to 60. Use a negative value (e.g. via an
	// ACL rule's rate_limit) to disable the per-peer cap entirely.
	RateLimitPerPeer float64 `json:"rate_limit_per_peer,omitempty"`
	// RateLimitGlobal caps total inbound swarm envelopes across peers, in
	// messages per minute. 0 (or unset) defaults to 600. Use a negative value
	// to disable the global cap entirely.
	RateLimitGlobal float64 `json:"rate_limit_global,omitempty"`
	// AuditLog appends swarm operations to the mesh audit trail
	// (~/.rhizome/mesh-audit.jsonl) with "swarm." prefixed ops. Defaults true.
	AuditLog bool `json:"audit_log"`
	// ACL holds per-(swarm, peer) authorization rules for swarm operations.
	ACL []SwarmACLRule `json:"acl,omitempty"`
	// Coordination tunes the deterministic coordinator and shared state.
	Coordination SwarmCoordinationConfig `json:"coordination,omitempty"`
}

// SwarmCoordinationConfig controls coordinator election and shared state.
type SwarmCoordinationConfig struct {
	// Enabled turns coordinator election and shared-state writes on.
	// Defaults to true when swarm is enabled; set false to opt out.
	Enabled *bool `json:"enabled,omitempty"`
	// StateInterval is how often the coordinator refreshes shared state.
	// Defaults to 30s.
	StateInterval time.Duration `json:"state_interval,omitempty"`
}

// CoordinationEnabled reports whether coordinator election is on (default).
func (s SwarmConfig) CoordinationEnabled() bool {
	return s.Coordination.Enabled == nil || *s.Coordination.Enabled
}

func (c *SwarmCoordinationConfig) normalize() {
	if c.StateInterval <= 0 {
		c.StateInterval = 30 * time.Second
	}
}

func (c SwarmCoordinationConfig) MarshalJSON() ([]byte, error) {
	type Alias SwarmCoordinationConfig
	return json.Marshal(&struct {
		Alias
		StateInterval string `json:"state_interval,omitempty"`
	}{
		Alias:         Alias(c),
		StateInterval: c.StateInterval.String(),
	})
}

func (c *SwarmCoordinationConfig) UnmarshalJSON(data []byte) error {
	type Alias SwarmCoordinationConfig
	aux := &struct {
		Alias
		StateInterval string `json:"state_interval,omitempty"`
	}{Alias: Alias(*c)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if aux.StateInterval != "" {
		d, err := time.ParseDuration(aux.StateInterval)
		if err != nil {
			return err
		}
		c.StateInterval = d
	}
	return nil
}

// SwarmACLRule authorizes a single peer for swarm operations. A missing rule
// falls back to "trusted peers may offer and claim".
type SwarmACLRule struct {
	// SwarmID scopes the rule to one swarm; "*" matches all swarms.
	SwarmID string `json:"swarm_id"`
	// PeerID is the peer the rule applies to; "*" matches all trusted peers.
	PeerID string `json:"peer_id"`
	// AllowOffer controls whether the peer may publish offers to us.
	AllowOffer *bool `json:"allow_offer,omitempty"`
	// AllowClaim controls whether the peer may claim our offers.
	AllowClaim *bool `json:"allow_claim,omitempty"`
	// Agents restricts which agent ids offers may target ("*" for all).
	Agents []string `json:"agents,omitempty"`
	// RateLimit overrides the per-peer swarm message cap (per minute).
	// 0 uses the default; negative means unlimited.
	RateLimit float64 `json:"rate_limit,omitempty"`
}

// SwarmQueueConfig controls the swarm task offer/claim queue.
type SwarmQueueConfig struct {
	// OfferTTL is how long an unclaimed offer stays open. Defaults to 2m.
	OfferTTL time.Duration `json:"offer_ttl,omitempty"`
	// ClaimWindow is how long the offerer collects claims before assigning.
	// Defaults to 5s.
	ClaimWindow time.Duration `json:"claim_window,omitempty"`
	// MaxOffers bounds concurrently tracked local offers. Defaults to 100.
	MaxOffers int `json:"max_offers,omitempty"`
	// AssignTimeout bounds how long an assigned remote task may run before
	// the offer is considered stalled and re-offered. Defaults to 10m.
	AssignTimeout time.Duration `json:"assign_timeout,omitempty"`
	// MaxRetries is how many times a failed/stalled offer is re-broadcast
	// before it lands in dead_letter state. 0/unset means 1; negative
	// disables retries entirely.
	MaxRetries int `json:"max_retries,omitempty"`
}

func (q *SwarmQueueConfig) normalize() {
	if q.OfferTTL <= 0 {
		q.OfferTTL = 2 * time.Minute
	}
	if q.ClaimWindow <= 0 {
		q.ClaimWindow = 5 * time.Second
	}
	if q.MaxOffers <= 0 {
		q.MaxOffers = 100
	}
	if q.AssignTimeout <= 0 {
		q.AssignTimeout = 10 * time.Minute
	}
	if q.MaxRetries == 0 {
		q.MaxRetries = 1
	} else if q.MaxRetries < 0 {
		q.MaxRetries = 0 // negative: retries disabled
	}
}

func (q SwarmQueueConfig) MarshalJSON() ([]byte, error) {
	type Alias SwarmQueueConfig
	return json.Marshal(&struct {
		Alias
		OfferTTL      string `json:"offer_ttl,omitempty"`
		ClaimWindow   string `json:"claim_window,omitempty"`
		AssignTimeout string `json:"assign_timeout,omitempty"`
	}{
		Alias:         Alias(q),
		OfferTTL:      q.OfferTTL.String(),
		ClaimWindow:   q.ClaimWindow.String(),
		AssignTimeout: q.AssignTimeout.String(),
	})
}

func (q *SwarmQueueConfig) UnmarshalJSON(data []byte) error {
	type Alias SwarmQueueConfig
	aux := &struct {
		Alias
		OfferTTL      string `json:"offer_ttl,omitempty"`
		ClaimWindow   string `json:"claim_window,omitempty"`
		AssignTimeout string `json:"assign_timeout,omitempty"`
	}{Alias: Alias(*q)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if aux.OfferTTL != "" {
		d, err := time.ParseDuration(aux.OfferTTL)
		if err != nil {
			return err
		}
		q.OfferTTL = d
	}
	if aux.ClaimWindow != "" {
		d, err := time.ParseDuration(aux.ClaimWindow)
		if err != nil {
			return err
		}
		q.ClaimWindow = d
	}
	if aux.AssignTimeout != "" {
		d, err := time.ParseDuration(aux.AssignTimeout)
		if err != nil {
			return err
		}
		q.AssignTimeout = d
	}
	return nil
}

// SwarmPresenceConfig controls swarm presence heartbeats.
type SwarmPresenceConfig struct {
	// HeartbeatInterval is how often a joined member broadcasts a PING.
	// Defaults to 15s.
	HeartbeatInterval time.Duration `json:"heartbeat_interval,omitempty"`
	// ExpireAfter is how long a member may stay silent before it is evicted
	// from the roster. Defaults to 45s.
	ExpireAfter time.Duration `json:"expire_after,omitempty"`
}

func (p *SwarmPresenceConfig) normalize() {
	if p.HeartbeatInterval <= 0 {
		p.HeartbeatInterval = 15 * time.Second
	}
	if p.ExpireAfter <= 0 {
		p.ExpireAfter = 45 * time.Second
	}
}

func (p SwarmPresenceConfig) MarshalJSON() ([]byte, error) {
	type Alias SwarmPresenceConfig
	return json.Marshal(&struct {
		Alias
		HeartbeatInterval string `json:"heartbeat_interval,omitempty"`
		ExpireAfter       string `json:"expire_after,omitempty"`
	}{
		Alias:             Alias(p),
		HeartbeatInterval: p.HeartbeatInterval.String(),
		ExpireAfter:       p.ExpireAfter.String(),
	})
}

func (p *SwarmPresenceConfig) UnmarshalJSON(data []byte) error {
	type Alias SwarmPresenceConfig
	aux := &struct {
		Alias
		HeartbeatInterval string `json:"heartbeat_interval,omitempty"`
		ExpireAfter       string `json:"expire_after,omitempty"`
	}{Alias: Alias(*p)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if aux.HeartbeatInterval != "" {
		d, err := time.ParseDuration(aux.HeartbeatInterval)
		if err != nil {
			return err
		}
		p.HeartbeatInterval = d
	}
	if aux.ExpireAfter != "" {
		d, err := time.ParseDuration(aux.ExpireAfter)
		if err != nil {
			return err
		}
		p.ExpireAfter = d
	}
	return nil
}

// DefaultSwarmConfig returns a disabled swarm config with default bounds.
func DefaultSwarmConfig() SwarmConfig {
	cfg := SwarmConfig{
		MaxMembers:       DefaultSwarmMaxMembers,
		MaxMessageBytes:  DefaultSwarmMaxMessageBytes,
		RequestMaxSkew:   DefaultSwarmRequestMaxSkew,
		Transport:        "direct",
		RateLimitPerPeer: 60,
		RateLimitGlobal:  600,
		AuditLog:         true,
	}
	cfg.Presence.normalize()
	cfg.Queue.normalize()
	cfg.Coordination.normalize()
	return cfg
}

// Normalize fills unset swarm fields with their defaults.
func (s *SwarmConfig) Normalize() {
	if s.MaxMembers <= 0 {
		s.MaxMembers = DefaultSwarmMaxMembers
	}
	if s.MaxMessageBytes <= 0 {
		s.MaxMessageBytes = DefaultSwarmMaxMessageBytes
	}
	if s.RequestMaxSkew <= 0 {
		s.RequestMaxSkew = DefaultSwarmRequestMaxSkew
	}
	if s.Transport == "" {
		s.Transport = "direct"
	}
	// 0 is the Go zero value and is ambiguous with "unset", so it defaults
	// to the documented cap. Use a negative value to opt out of a cap.
	if s.RateLimitPerPeer == 0 {
		s.RateLimitPerPeer = 60
	}
	if s.RateLimitGlobal == 0 {
		s.RateLimitGlobal = 600
	}
	// AuditLog defaults to true only through DefaultSwarmConfig; an explicit
	// false in config is honored (the field is not omitempty).
	s.Presence.normalize()
	s.Queue.normalize()
	s.Coordination.normalize()
}

func (s *SwarmConfig) MarshalJSON() ([]byte, error) {
	type Alias SwarmConfig
	return json.Marshal(&struct {
		*Alias
		RequestMaxSkew string `json:"request_max_skew,omitempty"`
	}{
		Alias:          (*Alias)(s),
		RequestMaxSkew: s.RequestMaxSkew.String(),
	})
}

func (s *SwarmConfig) UnmarshalJSON(data []byte) error {
	type Alias SwarmConfig
	aux := &struct {
		*Alias
		RequestMaxSkew string `json:"request_max_skew,omitempty"`
	}{
		Alias: (*Alias)(s),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if aux.RequestMaxSkew != "" {
		d, err := time.ParseDuration(aux.RequestMaxSkew)
		if err != nil {
			return err
		}
		s.RequestMaxSkew = d
	}
	return nil
}
