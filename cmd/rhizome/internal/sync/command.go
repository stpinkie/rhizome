package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	rsync "github.com/stpinkie/rhizome/pkg/rhizome/sync"
)

func NewSyncCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Workspace git sync commands",
		Long:  "Inspect and control the Rhizome workspace git repository and mesh sync.",
	}

	cmd.AddCommand(
		newSyncStatusCommand(),
		newSyncLogCommand(),
		newSyncCommitCommand(),
		newSyncPullCommand(),
		newSyncPushCommand(),
	)

	return cmd
}

func workspacePath() string {
	return filepath.Join(config.GetHome(), pkg.WorkspaceName)
}

func openRepo() (*git.Repository, *git.Worktree, error) {
	return rsync.OpenOrInit(workspacePath())
}

func newSyncStatusCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show workspace sync status",
		RunE: func(_ *cobra.Command, _ []string) error {
			repo, _, err := openRepo()
			if err != nil {
				return fmt.Errorf("open workspace: %w", err)
			}

			head, err := repo.Head()
			if err != nil {
				return fmt.Errorf("head: %w", err)
			}

			w, err := repo.Worktree()
			if err != nil {
				return err
			}

			status, err := w.Status()
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}

			conflicts, err := rsync.ConflictPaths(w)
			if err != nil {
				return fmt.Errorf("conflicts: %w", err)
			}

			syncStatus, _ := rsync.LoadSyncStatus(workspacePath())

			modifiedFiles := make([]string, 0, len(status))
			for path := range status {
				modifiedFiles = append(modifiedFiles, path)
			}

			out := struct {
				Head          string            `json:"head"`
				Branch        string            `json:"branch"`
				Workspace     string            `json:"workspace"`
				ModifiedFiles []string          `json:"modified_files,omitempty"`
				Conflicts     []string          `json:"conflicts,omitempty"`
				LastSyncError string            `json:"last_sync_error,omitempty"`
				LastErrorTime time.Time         `json:"last_error_time,omitempty"`
				PeerHeads     map[string]string `json:"peer_heads,omitempty"`
			}{
				Head:          head.Hash().String()[:8],
				Branch:        head.Name().Short(),
				Workspace:     "clean",
				ModifiedFiles: modifiedFiles,
				Conflicts:     conflicts,
				LastSyncError: syncStatus.LastSyncError,
				LastErrorTime: syncStatus.LastErrorTime,
				PeerHeads:     syncStatus.PeerHeads,
			}
			if !status.IsClean() {
				out.Workspace = "modified"
			}

			if jsonOutput {
				data, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal status: %w", err)
				}
				fmt.Println(string(data))
				return nil
			}

			fmt.Printf("HEAD: %s (%s)\n", out.Head, out.Branch)
			fmt.Printf("Workspace: %s\n", out.Workspace)
			if !status.IsClean() {
				for path, f := range status {
					fmt.Printf("  %c%c %s\n", f.Staging, f.Worktree, path)
				}
			}

			if len(conflicts) > 0 {
				fmt.Println("Conflicts:")
				for _, c := range conflicts {
					fmt.Printf("  %s\n", c)
				}
			}

			if syncStatus.LastSyncError != "" {
				fmt.Printf("Last sync error: %s (%s)\n", syncStatus.LastSyncError, syncStatus.LastErrorTime.Format(time.RFC3339))
			} else {
				fmt.Println("Last sync error: none")
			}

			if len(syncStatus.PeerHeads) > 0 {
				fmt.Println("Peer heads:")
				for pid, h := range syncStatus.PeerHeads {
					fmt.Printf("  %s: %s\n", pid, h)
				}
			} else {
				fmt.Println("Peer heads: none")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output status as JSON")

	return cmd
}

func newSyncLogCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "log",
		Short: "Show workspace git log",
		RunE: func(_ *cobra.Command, _ []string) error {
			repo, _, err := openRepo()
			if err != nil {
				return fmt.Errorf("open workspace: %w", err)
			}

			commits, err := repo.Log(&git.LogOptions{})
			if err != nil {
				return fmt.Errorf("log: %w", err)
			}
			defer commits.Close()

			err = commits.ForEach(func(c *object.Commit) error {
				fmt.Printf("%s %s\n", c.Hash.String()[:8], strings.Split(c.Message, "\n")[0])
				return nil
			})
			return err
		},
	}
}

func newSyncCommitCommand() *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit workspace changes now",
		RunE: func(_ *cobra.Command, _ []string) error {
			_, w, err := openRepo()
			if err != nil {
				return fmt.Errorf("open workspace: %w", err)
			}

			id, name, err := loadIdentity()
			if err != nil {
				return err
			}

			if message == "" {
				message = fmt.Sprintf("%s: manual sync", name)
			}

			hash, err := rsync.Commit(w, name, message)
			if err != nil {
				return fmt.Errorf("commit: %w", err)
			}
			_ = id
			fmt.Printf("Committed %s\n", hash.String()[:8])
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Commit message")
	return cmd
}

func newSyncPullCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pull <peer-multiaddr>",
		Short: "Pull and merge from a peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return withTemporaryNode(func(ctx context.Context, node *network.Node, syncer *rsync.Syncer) error {
				if err := node.Connect(ctx, args[0]); err != nil {
					return fmt.Errorf("connect: %w", err)
				}
				time.Sleep(200 * time.Millisecond)

				peers := node.ConnectedPeers()
				if len(peers) == 0 {
					return fmt.Errorf("no connected peer")
				}
				return syncer.PullFrom(ctx, peers[0])
			})
		},
	}
}

func newSyncPushCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "push <peer-multiaddr>",
		Short: "Commit and push to a peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return withTemporaryNode(func(ctx context.Context, node *network.Node, syncer *rsync.Syncer) error {
				if err := node.Connect(ctx, args[0]); err != nil {
					return fmt.Errorf("connect: %w", err)
				}
				time.Sleep(200 * time.Millisecond)

				peers := node.ConnectedPeers()
				if len(peers) == 0 {
					return fmt.Errorf("no connected peer")
				}
				_, err := syncer.PushTo(ctx, peers[0])
				return err
			})
		},
	}
}

func withTemporaryNode(fn func(context.Context, *network.Node, *rsync.Syncer) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	derived, name, err := loadIdentity()
	if err != nil {
		return err
	}

	node, err := network.NewNode(
		ctx,
		derived.Libp2pPrivKey,
		network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}},
	)
	if err != nil {
		return err
	}
	defer node.Close()

	workspace := workspacePath()
	syncer, err := rsync.NewSyncer(ctx, rsync.Config{
		Workspace:        workspace,
		NodeName:         name,
		Node:             node,
		AutoSync:         false,
		CommitInterval:   time.Hour,
		AnnounceInterval: time.Hour,
		Exclude:          config.DefaultSyncExclude,
	})
	if err != nil {
		return err
	}
	if err := syncer.Start(ctx); err != nil {
		return err
	}
	defer syncer.Stop()

	return fn(ctx, node, syncer)
}

func loadIdentity() (*identity.Derived, string, error) {
	home := config.GetHome()
	identityDir := filepath.Join(home, "identity")
	derived, name, err := internal.LoadIdentity(identityDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
		fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
		return nil, "", err
	}
	return derived, name, nil
}
