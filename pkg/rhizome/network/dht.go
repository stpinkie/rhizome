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
)

// DHTConfig controls when and how the public IPFS DHT is used.
type DHTConfig struct {
	Enabled           bool
	Server            bool
	Rendezvous        string
	BootstrapPeers    []string
	ReprovideInterval time.Duration
}

// Discovery provides DHT-based peer discovery for a Rhizome node.
type Discovery struct {
	host       host.Host
	dht        *dht.IpfsDHT
	rendezvous cid.Cid
	cfg        DHTConfig
	OnFound    func(peer.AddrInfo)

	cancel context.CancelFunc
	wg     sync.WaitGroup
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
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
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

func (d *Discovery) bootstrap(ctx context.Context) {
	bctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := d.dht.Bootstrap(bctx); err != nil {
		// Bootstrap is best-effort; DHT queries may still succeed later.
		_ = err
	}
}

func (d *Discovery) provideLoop(ctx context.Context) {
	const retry = 5 * time.Second
	var provided bool

	t := time.NewTimer(0)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := d.dht.Provide(pctx, d.rendezvous, true)
		cancel()
		if err == nil {
			provided = true
		}

		interval := d.cfg.ReprovideInterval
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		if !provided {
			interval = retry
		}
		t.Reset(interval)
	}
}

func (d *Discovery) discoverLoop(ctx context.Context) {
	const retry = 5 * time.Second
	var found bool

	// Wrap the OnFound callback so we can detect the first successful discovery.
	orig := d.OnFound
	d.OnFound = func(info peer.AddrInfo) {
		found = true
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
	fctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	providers, err := d.dht.FindProviders(fctx, d.rendezvous)
	if err != nil {
		return
	}

	for _, info := range providers {
		if info.ID == d.host.ID() {
			continue
		}

		if d.host.Network().Connectedness(info.ID) != network.Connected {
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
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
