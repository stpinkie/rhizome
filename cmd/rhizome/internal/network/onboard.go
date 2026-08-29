package network

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

func newOnboardCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Create a Rhizome node identity from a BIP39 mnemonic",
		Run: func(cmd *cobra.Command, args []string) {
			runOnboard()
		},
	}
}

func runOnboard() {
	home := config.GetHome()
	identityDir := filepath.Join(home, "identity")

	if _, err := os.Stat(filepath.Join(identityDir, "node.json")); err == nil {
		fmt.Printf("A node identity already exists at %s\n", identityDir)
		fmt.Print("Overwrite and re-derive? (y/n): ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("Aborted.")
			return
		}
	}

	mnemonic, err := promptHidden("Enter BIP39 mnemonic: ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading mnemonic: %v\n", err)
		os.Exit(1)
	}

	if mnemonic == "" {
		fmt.Fprintln(os.Stderr, "Mnemonic is required.")
		os.Exit(1)
	}

	name, err := promptText("Node name: ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading node name: %v\n", err)
		os.Exit(1)
	}
	if name == "" {
		name = "rhizome"
	}

	indexStr, err := promptText("Node index (0-based, default 0): ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading node index: %v\n", err)
		os.Exit(1)
	}

	nodeIndex := uint32(0)
	if indexStr != "" {
		i, err := strconv.ParseUint(indexStr, 10, 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid node index: %v\n", err)
			os.Exit(1)
		}
		nodeIndex = uint32(i)
	}

	derived, _, err := identity.FromMnemonic(strings.TrimSpace(mnemonic), nodeIndex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deriving identity: %v\n", err)
		os.Exit(1)
	}

	if err := identity.Save(identityDir, derived, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving identity: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n%s Node identity created\n", internal.Logo)
	fmt.Printf("  Name:      %s\n", name)
	fmt.Printf("  Index:     %d\n", nodeIndex)
	fmt.Printf("  Peer ID:   %s\n", derived.PeerID)
	fmt.Printf("  Saved to:  %s\n", identityDir)
	fmt.Println("\nBack up your BIP39 mnemonic. The node key alone cannot recover other nodes.")
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
