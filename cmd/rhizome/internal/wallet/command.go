// Package wallet implements the rhizome wallet command tree — local
// (daemonless) management of the encrypted web3 key store.
package wallet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// store opens the wallet under <RHIZOME_HOME>/web3, prompting for a
// passphrase on TTY when the store is scrypt-locked.
func store() (*web3.WalletStore, error) {
	s := web3.OpenWalletStore(web3.WalletDir(internal.GetRhizomeHome()))
	if _, err := s.List(); !errors.Is(err, web3.ErrWalletLocked) {
		return s, err
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		return nil, web3.ErrWalletLocked
	}
	fmt.Fprint(os.Stderr, "Wallet passphrase: ")
	b, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read passphrase: %w", err)
	}
	os.Setenv("RHIZOME_WALLET_PASSPHRASE", string(b))
	return s, nil
}

// NewWalletCommand returns the rhizome wallet command tree.
func NewWalletCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wallet",
		Short: "Manage the local web3 wallet (secp256k1 keys)",
		Long: "The wallet stores encrypted secp256k1 keys under ~/.rhizome/web3 " +
			"(OS keyring master key; RHIZOME_WALLET_PASSPHRASE fallback). Signing " +
			"requires tools.web3.signing.enabled and human approval of each " +
			"pending request — the agent can never move funds autonomously.",
	}
	cmd.AddCommand(
		newCreateCommand(),
		newImportCommand(),
		newListCommand(),
		newRevealCommand(),
	)
	return cmd
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func printJSON(w io.Writer, v any) {
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(w, string(out))
}

func newCreateCommand() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate a new wallet address",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			s, err := store()
			if err != nil {
				fatal(err)
			}
			e, err := s.Generate(label)
			if err != nil {
				fatal(err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", e.Address)
			fmt.Fprintln(cmd.OutOrStdout(),
				"Fund it on a testnet first — sends always require human approval, "+
					"but mainnet mistakes are irreversible.")
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "human label for the address")
	return cmd
}

func newImportCommand() *cobra.Command {
	var label string
	var useStdin bool
	cmd := &cobra.Command{
		Use:   "import [0x-private-key]",
		Short: "Import an existing private key",
		Long: "Import a 32-byte secp256k1 private key. Passing the key as an " +
			"argument exposes it to shell history — prefer --stdin or an " +
			"interactive prompt.",
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var hexKey string
			switch {
			case useStdin:
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					fatal(fmt.Errorf("read stdin: %w", err))
				}
				hexKey = strings.TrimSpace(string(data))
			case len(args) == 1:
				hexKey = args[0]
			case term.IsTerminal(os.Stdin.Fd()):
				fmt.Fprint(os.Stderr, "Private key (0x…, not echoed): ")
				b, err := term.ReadPassword(os.Stdin.Fd())
				fmt.Fprintln(os.Stderr)
				if err != nil {
					fatal(fmt.Errorf("read key: %w", err))
				}
				hexKey = strings.TrimSpace(string(b))
			default:
				fatal(fmt.Errorf("pass the key as an argument or --stdin"))
			}
			s, err := store()
			if err != nil {
				fatal(err)
			}
			e, err := s.Import(hexKey, label)
			if err != nil {
				fatal(err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported %s\n", e.Address)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "human label for the address")
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "read the private key from stdin")
	return cmd
}

func newListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List wallet addresses (never shows key material)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			s, err := store()
			if err != nil {
				fatal(err)
			}
			entries, err := s.List()
			if err != nil {
				fatal(err)
			}
			def, _ := s.Default()
			if asJSON {
				printJSON(cmd.OutOrStdout(), map[string]any{
					"addresses": entries, "default": def,
				})
				return
			}
			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(),
					"No wallet keys — `rhizome wallet create` or `rhizome wallet import`.")
				return
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ADDRESS\tLABEL\tDEFAULT\tCREATED")
			for _, e := range entries {
				mark := ""
				if e.Address == def {
					mark = "*"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					e.Address, e.Label, mark, e.CreatedAt.Format("2006-01-02"))
			}
			w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func newRevealCommand() *cobra.Command {
	var confirm, show bool
	cmd := &cobra.Command{
		Use:   "reveal <address>",
		Short: "Reveal a private key (requires explicit confirmation)",
		Long: "Prints the decrypted private key. Output is masked unless " +
			"--show is passed. Anyone holding the key controls the funds — " +
			"never paste it into chats, tickets, or prompts.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if !confirm {
				fatal(fmt.Errorf("reveal requires --confirm (and --show to print unmasked)"))
			}
			s, err := store()
			if err != nil {
				fatal(err)
			}
			key, err := s.Reveal(args[0])
			if err != nil {
				fatal(err)
			}
			if !show {
				masked := key
				if len(key) > 12 {
					masked = key[:6] + "…" + key[len(key)-4:]
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s (masked — pass --show for full key)\n", masked)
				return
			}
			fmt.Fprintln(cmd.OutOrStdout(), key)
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "confirm you want to expose key material")
	cmd.Flags().BoolVar(&show, "show", false, "print the full key instead of a masked prefix")
	return cmd
}
