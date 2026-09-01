package network

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cosmos/go-bip39"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

func newOnboardCommand() *cobra.Command {
	var (
		mnemonic       string
		generate       bool
		name           string
		nodeIndex      uint32
		encryption     string
		overwrite      bool
		nonInteractive bool
	)

	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Create a Rhizome node identity from a BIP39 mnemonic",
		Run: func(cmd *cobra.Command, args []string) {
			runOnboard(runOnboardConfig{
				mnemonic:       mnemonic,
				generate:       generate,
				name:           name,
				nodeIndex:      nodeIndex,
				encryption:     encryption,
				overwrite:      overwrite,
				nonInteractive: nonInteractive,
			})
		},
	}

	cmd.Flags().StringVarP(&mnemonic, "mnemonic", "m", "", "BIP39 mnemonic to derive the identity")
	cmd.Flags().BoolVarP(&generate, "generate", "g", false, "Generate a new 24-word BIP39 mnemonic")
	cmd.Flags().StringVarP(&name, "name", "n", "", "Node name (default rhizome)")
	cmd.Flags().Uint32VarP(&nodeIndex, "node-index", "i", 0, "SLIP-0010 node index")
	cmd.Flags().StringVarP(&encryption, "encrypt", "e", "", "Encryption: keyring, passphrase, none (default prompt)")
	cmd.Flags().BoolVarP(&overwrite, "yes", "y", false, "Overwrite an existing node identity")
	cmd.Flags().
		BoolVar(&nonInteractive, "non-interactive", false, "Fail if any required value is missing instead of prompting")

	return cmd
}

const identityPassphraseEnv = "RHIZOME_IDENTITY_PASSPHRASE"

type runOnboardConfig struct {
	mnemonic       string
	generate       bool
	name           string
	nodeIndex      uint32
	encryption     string
	overwrite      bool
	nonInteractive bool
}

func runOnboard(cfg runOnboardConfig) {
	home := config.GetHome()
	identityDir := filepath.Join(home, "identity")

	existing, err := loadExistingNodeIdentity(identityDir)
	if err == nil && !cfg.overwrite {
		if cfg.nonInteractive {
			fmt.Fprintf(os.Stderr, "A node identity already exists at %s\n", identityDir)
			os.Exit(1)
		}
		fmt.Printf("A node identity already exists at %s\n", identityDir)
		fmt.Print("Overwrite and re-derive? (y/n): ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	mnemonic := cfg.mnemonic
	if cfg.generate && mnemonic != "" {
		fmt.Fprintln(os.Stderr, "Cannot specify both --mnemonic and --generate")
		os.Exit(1)
	}

	if mnemonic == "" && !cfg.generate {
		if cfg.nonInteractive {
			fmt.Fprintln(os.Stderr, "--mnemonic or --generate is required in non-interactive mode")
			os.Exit(1)
		}

		var choice string
		choice, err = promptChoice("Create identity from [g]enerated mnemonic or [e]xisting? ", []string{"g", "e"})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading choice: %v\n", err)
			os.Exit(1)
		}
		cfg.generate = choice == "g"
	}

	if cfg.generate {
		var entropy []byte
		entropy, err = bip39.NewEntropy(256)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating entropy: %v\n", err)
			os.Exit(1)
		}
		mnemonic, err = bip39.NewMnemonic(entropy)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating mnemonic: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\nA new BIP39 mnemonic has been generated.")
		fmt.Println("Write it down and store it securely. It is the only way to recover this identity.")
		fmt.Println()
		fmt.Println(mnemonic)

		if !cfg.nonInteractive {
			fmt.Print("\nType 'yes' to confirm you have written it down: ")
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(confirm) != "yes" {
				fmt.Println("Aborted.")
				os.Exit(1)
			}
		}
	}

	if mnemonic == "" {
		if cfg.nonInteractive {
			fmt.Fprintln(os.Stderr, "Mnemonic is required.")
			os.Exit(1)
		}
		mnemonic, err = promptHidden("Enter BIP39 mnemonic: ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading mnemonic: %v\n", err)
			os.Exit(1)
		}
	}

	if !bip39.IsMnemonicValid(mnemonic) {
		fmt.Fprintln(os.Stderr, "Invalid BIP39 mnemonic.")
		os.Exit(1)
	}

	name := cfg.name
	if name == "" {
		if cfg.nonInteractive {
			name = "rhizome"
		} else {
			name, err = promptText("Node name (default rhizome): ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading node name: %v\n", err)
				os.Exit(1)
			}
		}
	}
	if name == "" {
		name = "rhizome"
	}

	nodeIndex := cfg.nodeIndex
	if !cfg.nonInteractive && cfg.nodeIndex == 0 && !cfg.overwrite {
		var indexStr string
		indexStr, err = promptText("Node index (0-based, default 0): ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading node index: %v\n", err)
			os.Exit(1)
		}
		if indexStr != "" {
			var i uint64
			i, err = strconv.ParseUint(indexStr, 10, 32)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid node index: %v\n", err)
				os.Exit(1)
			}
			nodeIndex = uint32(i)
		}
	}

	if existing != nil && !cfg.overwrite && existing.NodeIndex == nodeIndex {
		fmt.Fprintf(os.Stderr, "Node index %d is already in use by the identity at %s\n", nodeIndex, identityDir)
		os.Exit(1)
	}

	derived, _, err := identity.FromMnemonic(strings.TrimSpace(mnemonic), nodeIndex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deriving identity: %v\n", err)
		os.Exit(1)
	}

	encryption := cfg.encryption
	if encryption == "" && !cfg.nonInteractive {
		choice, err := promptChoice(
			"Encrypt identity with [k]eyring, [p]assphrase, or [n]one? ",
			[]string{"k", "p", "n"},
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading encryption choice: %v\n", err)
			os.Exit(1)
		}
		switch choice {
		case "k":
			encryption = "keyring"
		case "p":
			encryption = "passphrase"
		default:
			encryption = "none"
		}
	}
	if encryption == "" {
		encryption = "none"
	}

	var passphrase string
	if encryption == "passphrase" {
		passphrase = os.Getenv(identityPassphraseEnv)
		if passphrase == "" && !cfg.nonInteractive {
			var err error
			passphrase, err = promptHidden("Set identity passphrase: ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading passphrase: %v\n", err)
				os.Exit(1)
			}
		}
		if passphrase == "" {
			fmt.Fprintf(
				os.Stderr,
				"Passphrase is required for --encrypt passphrase. Set %s or run interactively.\n",
				identityPassphraseEnv,
			)
			os.Exit(1)
		}
	}

	switch encryption {
	case "none":
		if err := identity.Save(identityDir, derived, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving identity: %v\n", err)
			os.Exit(1)
		}
	case "keyring":
		if err := identity.SaveEncryptedWithKeyring(identityDir, derived, name); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving encrypted identity to keyring: %v\n", err)
			os.Exit(1)
		}
	case "passphrase":
		if err := identity.SaveEncryptedWithPassphrase(identityDir, derived, name, passphrase); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving encrypted identity: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown encryption %q. Use keyring, passphrase, or none.\n", encryption)
		os.Exit(1)
	}

	fmt.Printf("\n%s Node identity created\n", internal.Logo)
	fmt.Printf("  Name:      %s\n", name)
	fmt.Printf("  Index:     %d\n", nodeIndex)
	fmt.Printf("  Peer ID:   %s\n", derived.PeerID)
	fmt.Printf("  Encrypted: %s\n", encryption)
	fmt.Printf("  Saved to:  %s\n", identityDir)
	if cfg.generate {
		fmt.Println("\nBack up your BIP39 mnemonic. The node key alone cannot recover other nodes.")
	}
}

func loadExistingNodeIdentity(identityDir string) (*identity.NodeIdentity, error) {
	path := filepath.Join(identityDir, "node.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ni identity.NodeIdentity
	if err := json.Unmarshal(data, &ni); err != nil {
		return nil, err
	}
	return &ni, nil
}

func promptHidden(prompt string) (string, error) {
	fmt.Print(prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func promptText(prompt string) (string, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func promptChoice(prompt string, choices []string) (string, error) {
	for {
		fmt.Print(prompt)
		reader := bufio.NewReader(os.Stdin)
		text, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(strings.ToLower(text))
		for _, c := range choices {
			if text == c {
				return c, nil
			}
		}
		fmt.Println("Invalid choice.")
	}
}
