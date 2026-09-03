package network

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

// DHTConfig controls when and how the public IPFS DHT is used.
type DHTConfig struct {
	Enabled           bool
	Server            bool
	Rendezvous        string
	BootstrapPeers    []string
	ReprovideInterval time.Duration
	DialTimeout       time.Duration
	QueryTimeout      time.Duration
	RetryInterval     time.Duration
}

// Validate checks the DHT configuration for common errors.
func (c DHTConfig) Validate() error {
	if c.Enabled && c.Rendezvous == "" {
		return fmt.Errorf("dht rendezvous is required when DHT is enabled")
	}
	if c.ReprovideInterval < 0 {
		return fmt.Errorf("dht reprovide_interval must not be negative")
	}
	if c.DialTimeout < 0 {
		return fmt.Errorf("dht dial_timeout must not be negative")
	}
	if c.QueryTimeout < 0 {
		return fmt.Errorf("dht query_timeout must not be negative")
	}
	if c.RetryInterval < 0 {
		return fmt.Errorf("dht retry_interval must not be negative")
	}
	return nil
}

// DHTStatus is a snapshot of DHT state for observability.
type DHTStatus struct {
	Rendezvous          string    `json:"rendezvous"`
	RendezvousCID       string    `json:"rendezvous_cid"`
	Mode                string    `json:"mode"`
	RoutingTableSize    int       `json:"routing_table_size"`
	BootstrapPeers      int       `json:"bootstrap_peers"`
	DiscoveredPeerCount int       `json:"discovered_peer_count"`
	LastProvideTime     time.Time `json:"last_provide_time,omitempty"`
	LastDiscoverTime    time.Time `json:"last_discover_time,omitempty"`
	HasProvided         bool      `json:"has_provided"`
	HasDiscovered       bool      `json:"has_discovered"`
}

// Discovery provides DHT-based peer discovery for a Rhizome node.
type Discovery struct {
	host       host.Host
	dht        *dht.IpfsDHT
	rendezvous cid.Cid
	cfg        DHTConfig
	OnFound    func(peer.AddrInfo)

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	eventBus runtimeevents.Bus

	mu               sync.RWMutex
	lastProvideTime  time.Time
	lastDiscoverTime time.Time
	discoveredCount  int
	provided         bool
	found            bool
}

// NewDiscovery creates a DHT discovery manager for the given host.
func NewDiscovery(h host.Host, cfg DHTConfig) (*Discovery, error) {
	rendezvous, err := rendezvousCID(cfg.Rendezvous)
	if err != nil {
		return nil, fmt.Errorf("derive rendezvous cid: %w", err)
	}

	return &Discovery{
		host:       h,
		rendezvous: rendezvous,
		cfg:        cfg,
	}, nil
}

// SetEventBus sets the runtime event bus used to publish DHT events.
func (d *Discovery) SetEventBus(bus runtimeevents.Bus) {
	d.eventBus = bus
}

// publishDHTEvent publishes a non-blocking mesh DHT event if a bus is configured.
func (d *Discovery) publishDHTEvent(kind runtimeevents.Kind, attrs map[string]any) {
	if d.eventBus == nil {
		return
	}
	severity := runtimeevents.SeverityInfo
	if kind == runtimeevents.KindMeshError {
		severity = runtimeevents.SeverityError
	}
	d.eventBus.PublishNonBlocking(runtimeevents.Event{
		Kind:     kind,
		Severity: severity,
		Source: runtimeevents.Source{
			Component: "dht",
		},
		Attrs: attrs,
	})
}

// Start creates the DHT, bootstraps it, and begins advertising/looking for peers.
func (d *Discovery) Start(ctx context.Context) error {
	mode := dht.ModeClient
	if d.cfg.Server {
		mode = dht.ModeServer
	}

	opts := []dht.Option{dht.Mode(mode)}
	if len(d.cfg.BootstrapPeers) > 0 {
		addrs, err := MultiaddrStrings(d.cfg.BootstrapPeers)
		if err != nil {
			return fmt.Errorf("parse dht bootstrap addrs: %w", err)
		}
		infos, err := peer.AddrInfosFromP2pAddrs(addrs...)
		if err != nil {
			return fmt.Errorf("convert dht bootstrap addrs: %w", err)
		}
		opts = append(opts, dht.BootstrapPeers(infos...))

		// Pre-connect to configured DHT bootstraps so the initial provide has a route.
		dialTimeout := d.cfg.DialTimeout
		if dialTimeout <= 0 {
			dialTimeout = 15 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, dialTimeout)
		for _, info := range infos {
			_ = d.host.Connect(cctx, info)
		}
		cancel()
	}

	kad, err := dht.New(d.host, opts...)
	if err != nil {
		return fmt.Errorf("create dht: %w", err)
	}
	d.dht = kad

	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.bootstrap(ctx)
	}()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.provideLoop(ctx)
	}()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.discoverLoop(ctx)
	}()

	return nil
}

// Stop shuts down the DHT and background goroutines.
func (d *Discovery) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
	if d.dht != nil {
		return d.dht.Close()
	}
	return nil
}

// Status returns a snapshot of the current DHT state.
func (d *Discovery) Status() DHTStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	mode := "client"
	if d.cfg.Server {
		mode = "server"
	}

	s := DHTStatus{
		Rendezvous:          d.cfg.Rendezvous,
		RendezvousCID:       d.rendezvous.String(),
		Mode:                mode,
		BootstrapPeers:      len(d.cfg.BootstrapPeers),
		DiscoveredPeerCount: d.discoveredCount,
		LastProvideTime:     d.lastProvideTime,
		LastDiscoverTime:    d.lastDiscoverTime,
		HasProvided:         d.provided,
		HasDiscovered:       d.found,
	}

	if d.dht != nil && d.dht.RoutingTable() != nil {
		s.RoutingTableSize = d.dht.RoutingTable().Size()
	}

	return s
}

func (d *Discovery) bootstrap(ctx context.Context) {
	start := time.Now()
	d.publishDHTEvent(runtimeevents.KindMeshDHTBootstrapStart, map[string]any{
		"rendezvous": d.cfg.Rendezvous,
	})

	queryTimeout := d.cfg.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = 60 * time.Second
	}
	bctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	err := d.dht.Bootstrap(bctx)
	if err != nil {
		d.publishDHTEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":      "dht.bootstrap",
			"error":      err.Error(),
			"rendezvous": d.cfg.Rendezvous,
		})
	}

	d.publishDHTEvent(runtimeevents.KindMeshDHTBootstrapDone, map[string]any{
		"rendezvous":  d.cfg.Rendezvous,
		"duration_ms": time.Since(start).Milliseconds(),
		"success":     err == nil,
	})
}

func (d *Discovery) provideLoop(ctx context.Context) {
	retry := d.cfg.RetryInterval
	if retry <= 0 {
		retry = 5 * time.Second
	}
	var localProvided bool
	var consecutiveErrors int

	t := time.NewTimer(0)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		queryTimeout := d.cfg.QueryTimeout
		if queryTimeout <= 0 {
			queryTimeout = 60 * time.Second
		}
		pctx, cancel := context.WithTimeout(ctx, queryTimeout)
		err := d.dht.Provide(pctx, d.rendezvous, true)
		cancel()

		d.mu.Lock()
		d.lastProvideTime = time.Now()
		if err == nil {
			localProvided = true
			d.provided = true
			consecutiveErrors = 0
		} else {
			consecutiveErrors++
		}
		d.mu.Unlock()

		if err != nil {
			d.publishDHTEvent(runtimeevents.KindMeshError, map[string]any{
				"stage":              "dht.provide",
				"error":              err.Error(),
				"rendezvous":         d.cfg.Rendezvous,
				"consecutive_errors": consecutiveErrors,
			})
		}

		interval := d.cfg.ReprovideInterval
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		if !localProvided {
			interval = retry
		} else if consecutiveErrors > 0 {
			// Back off on repeated provide errors without losing the normal cadence.
			backoff := retry * time.Duration(consecutiveErrors)
			if backoff > interval {
				backoff = interval
			}
			interval = backoff
		}
		t.Reset(interval)
	}
}

func (d *Discovery) discoverLoop(ctx context.Context) {
	retry := d.cfg.RetryInterval
	if retry <= 0 {
		retry = 5 * time.Second
	}
	var found bool

	// Wrap the OnFound callback so we can detect the first successful discovery
	// and update internal status counters.
	orig := d.OnFound
	d.OnFound = func(info peer.AddrInfo) {
		found = true
		d.mu.Lock()
		d.found = true
		d.mu.Unlock()
		if orig != nil {
			orig(info)
		}
	}

	t := time.NewTimer(retry)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		d.findAndDialPeers(ctx)

		interval := d.cfg.ReprovideInterval
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		if !found {
			interval = retry
		} else {
			interval = interval / 2
		}
		t.Reset(interval)
	}
}

func (d *Discovery) findAndDialPeers(ctx context.Context) {
	queryTimeout := d.cfg.QueryTimeout
	if queryTimeout <= 0 {
		queryTimeout = 60 * time.Second
	}
	fctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	providers, err := d.dht.FindProviders(fctx, d.rendezvous)
	if err != nil {
		d.publishDHTEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":      "dht.find_providers",
			"error":      err.Error(),
			"rendezvous": d.cfg.Rendezvous,
		})
		return
	}

	d.mu.Lock()
	d.lastDiscoverTime = time.Now()
	d.discoveredCount += len(providers)
	d.mu.Unlock()

	for _, info := range providers {
		if info.ID == d.host.ID() {
			continue
		}

		d.publishDHTEvent(runtimeevents.KindMeshDHTDiscovered, map[string]any{
			"peer_id":     info.ID.String(),
			"addrs_count": len(info.Addrs),
		})

		if d.host.Network().Connectedness(info.ID) != network.Connected {
			dialTimeout := d.cfg.DialTimeout
			if dialTimeout <= 0 {
				dialTimeout = 15 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, dialTimeout)
			_ = d.host.Connect(cctx, info)
			cancel()
		}

		if d.OnFound != nil {
			d.OnFound(info)
		}
	}
}

// rendezvousCID converts an arbitrary string to a CID that the DHT can provide/find.
func rendezvousCID(s string) (cid.Cid, error) {
	h := sha256.Sum256([]byte(s))
	mh, err := multihash.Encode(h[:], multihash.SHA2_256)
	if err != nil {
		return cid.Cid{}, err
	}
	return cid.NewCidV1(cid.Raw, mh), nil
}
