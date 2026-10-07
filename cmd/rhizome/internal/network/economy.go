package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
)

// NewEconomyCommand returns the paired-settlement verb group (Track 141).
// Reads (balance, ledger, invoice) are daemonless — the bilateral ledger
// file under RHIZOME_HOME is the source of truth. Mutations prefer the
// daemon (POST /network/economy/*) so transitions emit mesh.econ.* events
// and refresh the live index; when the daemon is down they fall back to a
// direct ledger write — the append-only JSONL tolerates both writers.
func NewEconomyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "economy",
		Short: "Paired-settlement ledger verbs (balances, invoices, settle, disputes)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newEconomyBalanceCommand(),
		newEconomyLedgerCommand(),
		newEconomyInvoiceCommand(),
		newEconomySettleCommand(),
		newEconomyDisputeCommand(),
		newEconomyResolveCommand(),
	)
	return cmd
}

// ledgerPath locates the bilateral ledger under RHIZOME_HOME.
func ledgerPath() string {
	return filepath.Join(config.GetHome(), econ.LedgerFileName)
}

// readLedger loads the daemonless entry stream (missing file = empty).
func readLedger() ([]econ.Entry, error) {
	return econ.ReadEntries(ledgerPath())
}

// indexEntries folds the stream into the map BalanceEntries consumes.
func indexEntries(entries []econ.Entry) map[string]*econ.Entry {
	out := make(map[string]*econ.Entry, len(entries))
	for i := range entries {
		out[entries[i].EntryID] = &entries[i]
	}
	return out
}

func newEconomyBalanceCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "balance [peer-id]",
		Short: "Show accrued/settled balances per peer and unit",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			entries, err := readLedger()
			if err != nil {
				return err
			}
			idx := indexEntries(entries)
			if len(args) == 1 {
				pid := args[0]
				if _, err := peer.Decode(pid); err != nil {
					return fmt.Errorf("invalid peer id %q", pid)
				}
				return printBalances(w, map[string][]econ.UnitBalance{
					pid: econ.BalanceEntries(idx, func(e *econ.Entry) bool {
						return e.PeerID == pid
					}),
				}, asJSON)
			}
			peers := map[string]bool{}
			for _, e := range entries {
				peers[e.PeerID] = true
			}
			out := make(map[string][]econ.UnitBalance, len(peers))
			for p := range peers {
				out[p] = econ.BalanceEntries(idx, func(e *econ.Entry) bool {
					return e.PeerID == p
				})
			}
			return printBalances(w, out, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func printBalances(w io.Writer, bals map[string][]econ.UnitBalance, asJSON bool) error {
	if asJSON {
		enc, err := json.MarshalIndent(bals, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(enc))
		return nil
	}
	peers := make([]string, 0, len(bals))
	for p := range bals {
		peers = append(peers, p)
	}
	sort.Strings(peers)
	if len(peers) == 0 {
		fmt.Fprintln(w, "No ledger entries.")
		return nil
	}
	for _, p := range peers {
		fmt.Fprintf(w, "Peer %s\n", p)
		if len(bals[p]) == 0 {
			fmt.Fprintln(w, "  (no balances)")
			continue
		}
		for _, b := range bals[p] {
			fmt.Fprintf(w,
				"  %-10s payable:   accrued %-12s settled %-12s\n  %-10s receivable: accrued %-12s settled %-12s\n",
				b.Unit, b.PayableAccrued, b.PayableSettled,
				"", b.ReceivableAccrued, b.ReceivableSettled)
		}
	}
	return nil
}

func newEconomyLedgerCommand() *cobra.Command {
	var peerID, since, state string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Show the bilateral ledger entry stream",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			entries, err := readLedger()
			if err != nil {
				return err
			}
			var cutoff time.Time
			if since != "" {
				cutoff, err = parseSince(since)
				if err != nil {
					return err
				}
			}
			var states []econ.EntryState
			if state != "" {
				for _, s := range splitCSV(state) {
					st := econ.EntryState(s)
					switch st {
					case econ.StateAccrued, econ.StateSettled,
						econ.StateDisputed, econ.StateWrittenOff:
						states = append(states, st)
					default:
						return fmt.Errorf("invalid state %q (accrued|settled|disputed|written_off)", s)
					}
				}
			}
			out := make([]econ.Entry, 0, len(entries))
			for _, e := range entries {
				if peerID != "" && e.PeerID != peerID {
					continue
				}
				if !cutoff.IsZero() && e.TS.Before(cutoff) {
					continue
				}
				if len(states) > 0 && !stateIn(e.State, states) {
					continue
				}
				out = append(out, e)
			}
			if asJSON {
				enc, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(enc))
				return nil
			}
			if len(out) == 0 {
				fmt.Fprintln(w, "No matching ledger entries.")
				return nil
			}
			for _, e := range out {
				line := fmt.Sprintf("%s  %-11s %-9s %-6s %10s",
					e.TS.Local().Format("2006-01-02 15:04:05"),
					e.PeerID, e.Direction, e.State, e.Amount+" "+e.Unit)
				ref := e.TaskID
				if ref == "" {
					ref = e.CorrelationID
				}
				if ref != "" {
					line += "  ref=" + ref
				}
				if e.SettleID != "" {
					line += "  settle=" + e.SettleID
				}
				if e.Note != "" {
					line += "  note=" + e.Note
				}
				fmt.Fprintln(w, line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&peerID, "peer", "", "filter to this peer id")
	cmd.Flags().StringVar(&since, "since", "", "only entries newer than this (duration like 24h, or RFC3339)")
	cmd.Flags().StringVar(&state, "state", "",
		"filter by state (comma-separated: accrued,settled,disputed,written_off)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func newEconomyInvoiceCommand() *cobra.Command {
	var unit string
	cmd := &cobra.Command{
		Use:   "invoice <peer-id>",
		Short: "Emit the signed settlement proposal a payee would verify (dry-run of the settle offer)",
		Long: "Build the /rhizome/econ/1.0.0 settle offer document over the accrued " +
			"payable entries owed to the peer and sign it with the local node identity. " +
			"The output is exactly what the Track 142 handshake will send — nothing is " +
			"transmitted and no ledger state changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			pid := args[0]
			if _, err := peer.Decode(pid); err != nil {
				return fmt.Errorf("invalid peer id %q", pid)
			}
			home := config.GetHome()
			d, _, err := internal.LoadIdentity(filepath.Join(home, "identity"))
			if err != nil {
				return fmt.Errorf("load node identity: %w", err)
			}
			entries, err := readLedger()
			if err != nil {
				return err
			}
			offers, err := buildInvoices(d.PeerID, pid, unit, entries)
			if err != nil {
				return err
			}
			for _, offer := range offers {
				if err := offer.Sign(d.PrivateKey); err != nil {
					return err
				}
				enc, err := json.MarshalIndent(offer, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(w, string(enc))
			}
			if len(offers) == 0 {
				fmt.Fprintf(w, "No accrued payable balance for peer %s.\n", pid)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&unit, "unit", "", "settle only this unit (default: every unit with an accrued payable)")
	return cmd
}

// buildInvoices produces one unsigned offer per owed unit.
func buildInvoices(issuer, pid, unit string, entries []econ.Entry) ([]*econ.SettleOffer, error) {
	units := map[string]bool{}
	for _, e := range entries {
		if e.PeerID == pid && e.Direction == econ.DirectionPayable &&
			e.State == econ.StateAccrued {
			units[e.Unit] = true
		}
	}
	if unit != "" {
		if !units[unit] {
			return nil, nil
		}
		units = map[string]bool{unit: true}
	}
	list := make([]string, 0, len(units))
	for u := range units {
		list = append(list, u)
	}
	sort.Strings(list)
	var offers []*econ.SettleOffer
	for _, u := range list {
		offer, err := econ.BuildOffer(issuer, pid, u, entries, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if offer != nil {
			offers = append(offers, offer)
		}
	}
	return offers, nil
}

func newEconomySettleCommand() *cobra.Command {
	var unit, backend string
	var markOnly, asJSON bool
	cmd := &cobra.Command{
		Use:   "settle <peer-id>",
		Short: "Settle accrued payable balances with a peer",
		Long: "Run the settlement path for the accrued payable balance owed to a peer. " +
			"The full handshake requires the daemon (Track 142); --mark-only writes a " +
			"local attested settlement marker instead — it attests the operator settled " +
			"out of band, and the peer's receivable view diverges until it settles on " +
			"its own or a future handshake closes the loop.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			pid := args[0]
			if _, err := peer.Decode(pid); err != nil {
				return fmt.Errorf("invalid peer id %q", pid)
			}
			if backend != "" && backend != "ledger" && backend != "web3" {
				return fmt.Errorf("backend must be ledger or web3")
			}
			if backend == "web3" {
				return econDaemonCall(w, "settle", map[string]any{
					"peer": pid, "unit": unit, "backend": backend, "mark_only": markOnly,
				}, asJSON)
			}
			if markOnly {
				// Prefer the daemon so the transition emits mesh.econ.settle
				// and refreshes the live index; daemonless fallback writes
				// the same marker via econ.MarkSettled.
				if err := econDaemonCall(w, "settle", map[string]any{
					"peer": pid, "unit": unit, "backend": "ledger", "mark_only": true,
				}, asJSON); err == nil {
					return nil
				} else if !errorsIsNoDaemon(err) {
					return err
				}
				return settleMarkOnly(w, pid, unit, asJSON)
			}
			return econDaemonCall(w, "settle", map[string]any{
				"peer": pid, "unit": unit, "backend": backend, "mark_only": false,
			}, asJSON)
		},
	}
	cmd.Flags().StringVar(&unit, "unit", "", "settle only this unit (required when several units are accrued)")
	cmd.Flags().StringVar(&backend, "backend", "ledger", "settlement backend: ledger|web3")
	cmd.Flags().BoolVar(&markOnly, "mark-only", false,
		"write a local attested settle marker without contacting the peer (divergence-risk fallback)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

// settleMarkOnly writes the local attested marker daemonless — the same
// econ.MarkSettled path the daemon endpoint uses, minus the event.
func settleMarkOnly(w io.Writer, pid, unit string, asJSON bool) error {
	home := config.GetHome()
	d, _, err := internal.LoadIdentity(filepath.Join(home, "identity"))
	if err != nil {
		return fmt.Errorf("load node identity: %w", err)
	}
	l, err := econ.OpenLedger(ledgerPath())
	if err != nil {
		return err
	}
	offer, entries, err := econ.MarkSettled(l, d.PeerID, pid, unit, d.PrivateKey)
	if err != nil {
		return err
	}
	if asJSON {
		enc, err := json.MarshalIndent(map[string]any{
			"mark_only": true,
			"peer":      pid,
			"settle_id": offer.ID(),
			"offer":     offer,
			"entries":   entries,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(enc))
		return nil
	}
	fmt.Fprintf(w, "Settled %d entr%s with %s (mark-only, settle id %s).\n",
		len(entries), plural(len(entries)), pid, offer.ID())
	fmt.Fprintln(w, "The peer's receivable view is unchanged — it diverges until it settles on its own.")
	return nil
}

func newEconomyDisputeCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "dispute <task-id> [reason]",
		Short: "Dispute accrued ledger entries for a task",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			reason := ""
			if len(args) == 2 {
				reason = args[1]
			}
			return econMutate(cmd.OutOrStdout(), "dispute", map[string]any{
				"task_id": args[0], "reason": reason,
			}, func(l *econ.Ledger) ([]econ.Entry, error) {
				return econ.DisputeTask(l, args[0], reason)
			}, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func newEconomyResolveCommand() *cobra.Command {
	var credit, drop, asJSON bool
	cmd := &cobra.Command{
		Use:   "resolve <task-id>",
		Short: "Resolve a dispute — --credit re-accrues, --drop writes off",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if credit == drop {
				return fmt.Errorf("exactly one of --credit or --drop is required")
			}
			action := "credit"
			if drop {
				action = "drop"
			}
			return econMutate(cmd.OutOrStdout(), "resolve", map[string]any{
				"task_id": args[0], "action": action,
			}, func(l *econ.Ledger) ([]econ.Entry, error) {
				return econ.ResolveTask(l, args[0], drop)
			}, asJSON)
		},
	}
	cmd.Flags().BoolVar(&credit, "credit", false, "re-accrue the disputed entries (the charge stands)")
	cmd.Flags().BoolVar(&drop, "drop", false, "write the disputed entries off permanently")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

// econMutate runs a ledger mutation: daemon POST first (events + live
// index), direct ledger write when the daemon is unreachable.
func econMutate(
	w io.Writer,
	op string, body map[string]any,
	local func(*econ.Ledger) ([]econ.Entry, error),
	asJSON bool,
) error {
	if err := econDaemonCall(w, op, body, asJSON); err == nil {
		return nil
	} else if !errorsIsNoDaemon(err) {
		return err
	}
	l, err := econ.OpenLedger(ledgerPath())
	if err != nil {
		return err
	}
	entries, err := local(l)
	if err != nil {
		return err
	}
	return printMutated(w, op, entries, asJSON)
}

// econDaemonCall POSTs to /network/economy/<op>; a transport-level
// "no running daemon" error signals the daemonless fallback.
func econDaemonCall(
	w io.Writer,
	op string, body map[string]any, asJSON bool,
) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, status, err := internal.DaemonRequest(
		http.MethodPost, "/network/economy/"+op, raw, 15*time.Second)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("daemon returned %d: %s", status, string(resp))
	}
	if asJSON {
		var pretty bytes.Buffer
		if json.Indent(&pretty, resp, "", "  ") == nil {
			fmt.Fprintln(w, pretty.String())
			return nil
		}
		fmt.Fprintln(w, string(resp))
		return nil
	}
	var parsed struct {
		Entries []econ.Entry `json:"entries"`
	}
	if json.Unmarshal(resp, &parsed) == nil && len(parsed.Entries) > 0 {
		return printMutated(w, op, parsed.Entries, false)
	}
	fmt.Fprintln(w, string(resp))
	return nil
}

func printMutated(
	w io.Writer,
	op string, entries []econ.Entry, asJSON bool,
) error {
	if asJSON {
		enc, err := json.MarshalIndent(map[string]any{
			"op":      op,
			"entries": entries,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(enc))
		return nil
	}
	for _, e := range entries {
		fmt.Fprintf(w, "%s  %-11s %-9s -> %s %s %s\n",
			op, e.PeerID, e.Direction, e.State, e.Amount, e.Unit)
	}
	fmt.Fprintf(w, "%d entr%s updated.\n", len(entries), plural(len(entries)))
	return nil
}

// errorsIsNoDaemon reports whether the daemon client could not reach a
// running daemon (the daemonless fallback trigger).
func errorsIsNoDaemon(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no running daemon")
}

func parseSince(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q (duration like 24h, or RFC3339)", s)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stateIn(s econ.EntryState, states []econ.EntryState) bool {
	for _, st := range states {
		if st == s {
			return true
		}
	}
	return false
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
