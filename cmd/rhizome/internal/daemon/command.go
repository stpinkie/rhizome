package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg"
	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/gateway"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/sync"
	"github.com/stpinkie/rhizome/pkg/skills"
)

func NewDaemonCommand() *cobra.Command {
	var listenAddrs []string
	var bootstrapPeers []string
	var debug bool
	var allowEmpty bool
	var noDHT bool
	var noGateway bool
	var syncCommitInterval time.Duration
	var syncAnnounceInterval time.Duration

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run a long-running Rhizome P2P node and gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			home := internal.GetRhizomeHome()
			identityDir := filepath.Join(home, "identity")

			derived, name, err := internal.LoadIdentity(identityDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
				fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
				return err
			}

			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			config.SetGlobal(cfg)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Shared runtime event bus for mesh, DHT, and agent events.
			eventBus := runtimeevents.NewBus()

			dhtEnabled := cfg.Mesh.DHTEnabled && !noDHT
			node, err := network.NewNode(ctx, derived.Libp2pPrivKey, network.Config{
				ListenAddrs:    listenAddrs,
				BootstrapPeers: bootstrapPeers,
				DHT: network.DHTConfig{
					Enabled:           dhtEnabled,
					Server:            cfg.Mesh.DHTServer,
					Rendezvous:        cfg.Mesh.DHTRendezvous,
					BootstrapPeers:    cfg.Mesh.DHTBootstrap,
					ReprovideInterval: cfg.Mesh.DHTReprovideInterval,
				},
				Timeouts: &cfg.Timeouts.Network,
			})
			if err != nil {
				return fmt.Errorf("failed to start Rhizome node: %w", err)
			}
			defer node.Close()
			node.SetEventBus(eventBus)

			workspace := filepath.Join(home, pkg.WorkspaceName)
			commitInterval := syncCommitInterval
			if commitInterval == 0 {
				commitInterval = config.DefaultSyncCommitInterval
			}
			announceInterval := syncAnnounceInterval
			if announceInterval == 0 {
				announceInterval = config.DefaultSyncAnnounceInterval
			}

			syncer, err := sync.NewSyncer(ctx, sync.Config{
				Workspace:        workspace,
				NodeName:         name,
				Node:             node,
				AutoSync:         true,
				CommitInterval:   commitInterval,
				AnnounceInterval: announceInterval,
				Exclude:          config.DefaultSyncExclude,
				Timeouts:         &cfg.Timeouts.Sync,
			})
			if err != nil {
				return fmt.Errorf("failed to open workspace sync: %w", err)
			}
			if err := syncer.Start(ctx); err != nil {
				return fmt.Errorf("failed to start syncer: %w", err)
			}
			defer syncer.Stop()

			var rhizomeMesh *mesh.Mesh
			if cfg.Mesh.Enabled {
				rhizomeMesh = mesh.NewMesh(node, syncer, derived, cfg.Mesh, nil)
				if cfg.Mesh.AdvertiseModels {
					rhizomeMesh.SetModelList(cfg.ModelList)
				}
				if cfg.Mesh.AdvertiseSkills {
					rhizomeMesh.SetSkillsLoader(skills.NewSkillsLoader(
						workspace,
						filepath.Join(home, "skills"),
						"",
					))
				}
				rhizomeMesh.SetEventBus(eventBus)
				if err := rhizomeMesh.Start(ctx); err != nil {
					return fmt.Errorf("failed to start mesh: %w", err)
				}
				defer rhizomeMesh.Stop()
			}

			fmt.Printf("%s Rhizome daemon online\n", internal.Logo)
			fmt.Printf("  Name:    %s\n", name)
			fmt.Printf("  Peer ID: %s\n", node.PeerID())
			fmt.Printf("  Workspace: %s\n", workspace)
			fmt.Printf("  Addrs:   %s\n", strings.Join(node.BootstrapAddrs(), ", "))

			if noGateway {
				<-ctx.Done()
				return nil
			}
			return gateway.RunWithMesh(debug, home, internal.GetConfigPath(), allowEmpty, rhizomeMesh, eventBus)
		},
	}

	cmd.Flags().StringArrayVar(&listenAddrs, "listen", []string{"/ip4/0.0.0.0/tcp/0"}, "Multiaddrs to listen on")
	cmd.Flags().StringArrayVar(&bootstrapPeers, "bootstrap", nil, "Bootstrap peer multiaddrs")
	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	cmd.Flags().
		BoolVarP(&allowEmpty, "allow-empty", "E", false, "Start gateway in limited mode without a default model")
	cmd.Flags().BoolVar(&noDHT, "no-dht", false, "Disable public DHT discovery")
	cmd.Flags().BoolVar(&noGateway, "no-gateway", false, "Do not start the HTTP gateway (useful for testing)")
	cmd.Flags().
		DurationVar(&syncCommitInterval, "sync-commit-interval", 0, "Interval between auto-sync commits (default 1m)")
	cmd.Flags().
		DurationVar(&syncAnnounceInterval, "sync-announce-interval", 0, "Interval between sync announcements (default 1m)")

	return cmd
}
