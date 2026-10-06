package market

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// NewMarketCommand returns the rhizome market command tree: thin verbs over
// the rhizome-market module's loopback API. The verbs carry their final
// argument shapes now; payload schemas flesh out in the market tracks.
func NewMarketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "market",
		Short: "Interact with the rhizome-market module (buy/sell agent work)",
		Long: "Thin client for the rhizome-market companion module's loopback " +
			"API. Install the module first: rhizome module install rhizome-market.",
	}
	cmd.AddCommand(
		newFindCommand(),
		newBuyCommand(),
		newSessionCommand(),
		newReceiptCommand(),
		newDisputeCommand(),
		newAttestCommand(),
		newEscalateCommand(),
		newEvidenceCommand(),
		newRefundCommand(),
		newReleaseCommand(),
	)
	return cmd
}

// runVerb resolves the module client and POSTs body to /v1/<verb>, printing
// the response verbatim. Non-2xx responses print the body and exit non-zero.
func runVerb(cmd *cobra.Command, verb string, body any) {
	client, err := resolveClient()
	if err != nil {
		fatal(err)
	}
	data, code, err := client.call(cmd.Context(), verb, body)
	if err != nil {
		fatal(err)
	}
	w := cmd.OutOrStdout()
	if code < 200 || code >= 300 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", string(data))
		os.Exit(1)
	}
	fmt.Fprintln(w, string(data))
}

func newFindCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "find <service>",
		Short: "Query the market index for a service",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "find", map[string]any{"query": args[0]})
		},
	}
}

func newBuyCommand() *cobra.Command {
	var maxCost, confirm string
	cmd := &cobra.Command{
		Use:   "buy <provider> <offer> <task>",
		Short: "Purchase a task from a market provider",
		Long: "Purchase a task from a market provider. The purchase is " +
			"asynchronous — settlement transactions queue into " +
			"web3-pending.json for approval by default. When export review " +
			"is required the response carries a review_id; confirm with " +
			"`rhizome market buy --confirm <review_id>` (provider/offer/task " +
			"args may be omitted — the stored decision is replayed).",
		Args: func(cmd *cobra.Command, args []string) error {
			if confirm != "" {
				if len(args) > 0 {
					return fmt.Errorf(
						"--confirm replays the stored purchase; provider/offer/task are ignored")
				}
				return nil
			}
			return cobra.ExactArgs(3)(cmd, args)
		},
		Run: func(cmd *cobra.Command, args []string) {
			body := map[string]any{}
			if confirm != "" {
				body["confirm_review_id"] = confirm
			} else {
				body["provider"] = args[0]
				body["offer"] = args[1]
				body["task"] = args[2]
				body["max_cost"] = maxCost
			}
			runVerb(cmd, "buy", body)
		},
	}
	cmd.Flags().StringVar(&maxCost, "max-cost", "", "Maximum spend for this purchase")
	cmd.Flags().StringVar(&confirm, "confirm", "", "Confirm a pending-review purchase by review id")
	return cmd
}

func newSessionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "session <id>",
		Short: "Show a market session's status",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "session", map[string]any{"id": args[0]})
		},
	}
}

func newReceiptCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "receipt <id>",
		Short: "Verify a stored market receipt",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "receipt", map[string]any{"id": args[0]})
		},
	}
}

func newDisputeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dispute <purchase-id-or-session> [reason]",
		Short: "Lock a purchase's escrow (buyer-initiated dispute)",
		Args:  cobra.RangeArgs(1, 2),
		Run: func(cmd *cobra.Command, args []string) {
			body := map[string]any{"id": args[0]}
			if len(args) > 1 {
				body["reason"] = args[1]
			}
			runVerb(cmd, "dispute", body)
		},
	}
}

func newAttestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attest",
		Short: "Issue, verify, or register a signed completion attestation",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "issue <purchase-id-or-session>",
			Short: "Sign a completion attestation for a terminal purchase",
			Long: "Issue a buyer-signed attestation for a completed, " +
				"resolved, refunded, or failed purchase. Strictly opt-in — " +
				"your signature is evidence you choose to give. The output " +
				"JSON is delivered to the seller out of band; they register " +
				"it with `market attest register`.",
			Args: cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				runVerb(cmd, "attest", map[string]any{"id": args[0]})
			},
		},
		&cobra.Command{
			Use:   "verify <attestation-json|@file>",
			Short: "Verify an attestation's signature and terms chain",
			Args:  cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				runVerb(cmd, "attest/verify",
					attestationBody(readArgOrFile(args[0])))
			},
		},
		&cobra.Command{
			Use:   "register <attestation-json|@file>",
			Short: "Store a received attestation (seller side)",
			Long: "Register a buyer-signed attestation into the local " +
				"store — newest-N land in the advertised reputation set.",
			Args: cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				runVerb(cmd, "attest/register",
					attestationBody(readArgOrFile(args[0])))
			},
		},
	)
	return cmd
}

// readArgOrFile returns the argument verbatim, or the file contents when
// the argument is an @path — attestation JSON is too long for a flag.
func readArgOrFile(arg string) []byte {
	if !strings.HasPrefix(arg, "@") {
		return []byte(arg)
	}
	data, err := os.ReadFile(strings.TrimPrefix(arg, "@"))
	if err != nil {
		fatal(fmt.Errorf("read %s: %w", arg, err))
	}
	return data
}

// attestationBody normalizes the input into the {attestation: {...}}
// request body — accepts a bare attestation object or the wrapped
// `attest issue` response verbatim.
func attestationBody(raw []byte) map[string]any {
	var wrapped map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapped) == nil {
		if inner, ok := wrapped["attestation"]; ok && len(inner) > 0 {
			return map[string]any{"attestation": inner}
		}
	}
	return map[string]any{"attestation": json.RawMessage(raw)}
}

func newEscalateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "escalate <purchase-id-or-session>",
		Short: "Escalate a disputed purchase to the Kleros arbitrator",
		Long: "Escalate a disputed purchase to the configured ERC-792 " +
			"arbitrator (escrow_arbiter=kleros:<court>). The caller funds " +
			"the arbitration fee in native token — the dispute-time " +
			"auto-escalation already tries this once; escalate retries it.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "escalate", map[string]any{"id": args[0]})
		},
	}
}

func newEvidenceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "evidence <purchase-id-or-session>",
		Short: "Show a purchase's ERC-1497 evidence bundle",
		Long: "Show the evidence bundle an arbiter reads for a purchase: " +
			"the signed _rhizome.receipt plus the terms hash committing " +
			"to the on-chain session facts.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "evidence", map[string]any{"id": args[0]})
		},
	}
}

func newRefundCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refund <purchase-id-or-session>",
		Short: "Withdraw a purchase's escrow funds after termination",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "refund", map[string]any{"id": args[0]})
		},
	}
}

func newReleaseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "release <purchase-id-or-session>",
		Short: "Release a verified purchase's escrow to the seller",
		Long: "Release a verified purchase's escrow to the seller. Only " +
			"needed when the module's buy_auto_release is off — verified " +
			"purchases then wait in awaiting_release for this call.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "release", map[string]any{"id": args[0]})
		},
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
