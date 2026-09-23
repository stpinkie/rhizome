// Package module implements the rhizome module command tree for managing
// companion sidecar modules.
package module

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/modules"
)

// manager builds a module manager bound to the user's config file.
func manager() (*modules.Manager, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, err
	}
	path := internal.GetConfigPath()
	return modules.NewManager(internal.GetRhizomeHome(), cfg, nil,
		func(c *config.Config) error { return config.SaveConfig(path, c) }), nil
}

// daemonUp reports whether a daemon is reachable for live lifecycle ops.
func daemonUp() bool {
	base, _ := internal.DaemonBaseURL()
	return base != ""
}

// daemonAction posts a mutating module action (install/uninstall/enable/
// disable/start/stop/restart) to the running daemon, which applies it against
// its in-memory manager AND persists — local writes would leave the daemon's
// status view stale until restart. Returns true when the daemon answered; a
// non-200 response is fatal since falling back to disk would desync it.
func daemonAction(id, action, version string, timeout time.Duration) bool {
	if !daemonUp() {
		return false
	}
	payload, _ := json.Marshal(map[string]string{"action": action, "version": version})
	data, code, err := internal.DaemonRequest(http.MethodPost, "/modules/"+id, payload, timeout)
	if err != nil {
		return false
	}
	if code != http.StatusOK {
		fatal(fmt.Errorf("%s", strings.TrimSpace(string(data))))
	}
	return true
}

// NewModuleCommand returns the rhizome module command tree.
func NewModuleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module",
		Short: "Manage companion modules (sidecar capabilities)",
		Long: "Companion modules extend Rhizome with sidecar binaries — they are " +
			"installed under ~/.rhizome/modules, verified by pinned sha256, and " +
			"supervised by the daemon.",
	}
	cmd.AddCommand(
		newListCommand(),
		newStatusCommand(),
		newInstallCommand(),
		newUninstallCommand(),
		newEnableCommand(true),
		newEnableCommand(false),
		newLifecycleCommand("start"),
		newLifecycleCommand("stop"),
		newLifecycleCommand("restart"),
		newLogsCommand(),
		newSetCommand(),
		newValidateCommand(),
		newVerifyCommand(),
		newCatalogCommand(),
		newCatalogKeygenCommand(),
	)
	return cmd
}

func printJSON(w io.Writer, v any) {
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(w, string(out))
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

// fetchInfo returns live module info — via the daemon when one is running
// (so running/unhealthy status is accurate), else from the local manager.
func fetchInfo(id string) (modules.Info, error) {
	if daemonUp() {
		data, code, err := internal.DaemonRequest(http.MethodGet, "/modules/"+id, nil, 10*time.Second)
		if err == nil && code == http.StatusOK {
			var info modules.Info
			if err := json.Unmarshal(data, &info); err == nil {
				return info, nil
			}
		}
	}
	mgr, err := manager()
	if err != nil {
		return modules.Info{}, err
	}
	return mgr.Info(id)
}

func fetchList() ([]modules.Info, error) {
	if daemonUp() {
		data, code, err := internal.DaemonRequest(http.MethodGet, "/modules", nil, 10*time.Second)
		if err == nil && code == http.StatusOK {
			var resp struct {
				Modules []modules.Info `json:"modules"`
			}
			if err := json.Unmarshal(data, &resp); err == nil {
				return resp.Modules, nil
			}
		}
	}
	mgr, err := manager()
	if err != nil {
		return nil, err
	}
	return mgr.List()
}

func newListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog modules and their status",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			infos, err := fetchList()
			if err != nil {
				fatal(err)
			}
			if asJSON {
				printJSON(w, infos)
				return
			}
			fmt.Fprintf(w, "%-24s %-10s %-14s %-8s %-9s %s\n", "ID", "KIND", "STATUS", "ENABLED", "SOURCE", "NAME")
			for _, i := range infos {
				enabled := ""
				if i.Enabled {
					enabled = "yes"
				}
				fmt.Fprintf(w, "%-24s %-10s %-14s %-8s %-9s %s\n",
					i.Spec.ID, i.Spec.Kind, i.Status, enabled, i.Source, i.Spec.Name)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

func newStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status <module-id>",
		Short: "Show detailed module status",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			info, err := fetchInfo(args[0])
			if err != nil {
				fatal(err)
			}
			if asJSON {
				printJSON(w, info)
				return
			}
			fmt.Fprintf(w, "%s — %s\n", info.Spec.ID, info.Spec.Name)
			fmt.Fprintf(w, "  kind:      %s\n", info.Spec.Kind)
			fmt.Fprintf(w, "  source:    %s\n", info.Source)
			fmt.Fprintf(w, "  status:    %s\n", info.Status)
			fmt.Fprintf(w, "  enabled:   %v\n", info.Enabled)
			if info.Version != "" {
				fmt.Fprintf(w, "  version:   %s\n", info.Version)
			}
			if info.InstalledPath != "" {
				fmt.Fprintf(w, "  path:      %s\n", info.InstalledPath)
			}
			if info.PID != 0 {
				fmt.Fprintf(w, "  pid:       %d (started %s)\n", info.PID,
					info.StartedAt.Format(time.RFC3339))
			}
			if info.Restarts > 0 {
				fmt.Fprintf(w, "  restarts:  %d (last exit: %s)\n", info.Restarts, info.LastExit)
			}
			if len(info.MissingFields) > 0 {
				fmt.Fprintf(w, "  missing:   %s\n", strings.Join(info.MissingFields, ", "))
			}
			if len(info.Spec.ConfigFields) > 0 {
				fmt.Fprintf(w, "  fields:\n")
				for _, f := range info.Spec.ConfigFields {
					val := info.Fields[f.Key]
					if f.Secret {
						set := ""
						for _, k := range info.SecretKeys {
							if k == f.Key {
								set = "yes"
							}
						}
						fmt.Fprintf(w, "    - %s (secret, set: %s)\n", f.Key, orNo(set))
						continue
					}
					fmt.Fprintf(w, "    - %s = %s\n", f.Key, orDefault(val, f.Default))
				}
			}
			if info.Spec.Notes != "" {
				fmt.Fprintf(w, "  notes:     %s\n", info.Spec.Notes)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

func orNo(s string) string {
	if s == "" {
		return "no"
	}
	return s
}

func orDefault(v, def string) string {
	if v == "" {
		if def == "" {
			return "(unset)"
		}
		return def + " (default)"
	}
	return v
}

func newInstallCommand() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "install <module-id>",
		Short: "Download, verify, and install a module release",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Installing %s…\n", args[0])
			if daemonAction(args[0], "install", version, 10*time.Minute) {
				info, _ := fetchInfo(args[0])
				fmt.Fprintf(w, "Installed %s v%s → %s\n", args[0], info.Version, info.InstalledPath)
				return
			}
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			if err := mgr.Install(context.Background(), args[0], version); err != nil {
				fatal(err)
			}
			info, _ := mgr.Info(args[0])
			fmt.Fprintf(w, "Installed %s v%s → %s\n", args[0], info.Version, info.InstalledPath)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "Pinned version to install (default: latest)")
	return cmd
}

func newUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <module-id>",
		Short: "Remove an installed module",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			if daemonAction(args[0], "uninstall", "", 30*time.Second) {
				fmt.Fprintf(w, "Uninstalled %s\n", args[0])
				return
			}
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			if err := mgr.Uninstall(args[0]); err != nil {
				fatal(err)
			}
			fmt.Fprintf(w, "Uninstalled %s\n", args[0])
		},
	}
}

func newEnableCommand(enable bool) *cobra.Command {
	name, short := "enable", "Enable"
	if !enable {
		name, short = "disable", "Disable"
	}
	return &cobra.Command{
		Use:   name + " <module-id>",
		Short: short + " a module (daemon-kind modules autostart with the daemon)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			if daemonAction(args[0], name, "", 30*time.Second) {
				fmt.Fprintf(w, "Module %s %sd\n", args[0], name)
				return
			}
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			if err := mgr.Enable(args[0], enable); err != nil {
				fatal(err)
			}
			fmt.Fprintf(w, "Module %s %sd\n", args[0], name)
		},
	}
}

// newLifecycleCommand builds start/stop/restart — all proxied to the daemon.
func newLifecycleCommand(action string) *cobra.Command {
	return &cobra.Command{
		Use: action + " <module-id>",
		Short: fmt.Sprintf("%s a module process (daemon required)",
			strings.ToUpper(action[:1])+action[1:]),
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			payload, _ := json.Marshal(map[string]string{"action": action})
			data, code, err := internal.DaemonRequest(http.MethodPost,
				"/modules/"+args[0], payload, 30*time.Second)
			if err != nil {
				fatal(fmt.Errorf("%v — lifecycle actions require a running daemon (rhizome daemon)", err))
			}
			if code != http.StatusOK {
				fatal(fmt.Errorf("%s", strings.TrimSpace(string(data))))
			}
			fmt.Fprintf(w, "Module %s: %s\n", args[0], strings.TrimSpace(string(data)))
		},
	}
}

func newLogsCommand() *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use:   "logs <module-id>",
		Short: "Show module stdout/stderr logs",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			stdout, stderr, err := mgr.Logs(args[0], tail)
			if err != nil {
				fatal(err)
			}
			if stdout == "" && stderr == "" {
				fmt.Fprintf(w, "No logs for %s.\n", args[0])
				return
			}
			if stdout != "" {
				fmt.Fprintf(w, "── stdout ──\n%s\n", stdout)
			}
			if stderr != "" {
				fmt.Fprintf(w, "── stderr ──\n%s\n", stderr)
			}
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 200, "Lines to show (max 2000)")
	return cmd
}

// newSetCommand writes module fields (or secrets with --secret).
func newSetCommand() *cobra.Command {
	var secret bool
	cmd := &cobra.Command{
		Use:   "set <module-id> <key>=<value> [key=value...]",
		Short: "Set module config fields (use --secret for secret fields)",
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			kv := map[string]string{}
			for _, arg := range args[1:] {
				k, v, ok := strings.Cut(arg, "=")
				if !ok || k == "" {
					fatal(fmt.Errorf("expected key=value, got %q", arg))
				}
				kv[k] = v
			}
			if daemonUp() {
				sub := "fields"
				if secret {
					sub = "secrets"
				}
				payload, _ := json.Marshal(kv)
				data, code, err := internal.DaemonRequest(http.MethodPut,
					"/modules/"+args[0]+"/"+sub, payload, 30*time.Second)
				if err == nil {
					if code != http.StatusOK {
						fatal(fmt.Errorf("%s", strings.TrimSpace(string(data))))
					}
					reportSet(w, args[0], kv, secret)
					return
				}
			}
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			if secret {
				err = mgr.SetSecrets(args[0], kv)
			} else {
				err = mgr.SetFields(args[0], kv)
			}
			if err != nil {
				fatal(err)
			}
		},
	}
	cmd.Flags().BoolVar(&secret, "secret", false, "Write to module secrets (.security.yml)")
	return cmd
}

// reportSet prints the post-write confirmation for `module set`.
func reportSet(w io.Writer, id string, kv map[string]string, secret bool) {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if secret {
		fmt.Fprintf(w, "Module %s secrets updated: %s (stored in .security.yml)\n",
			id, strings.Join(keys, ", "))
	} else {
		fmt.Fprintf(w, "Module %s fields updated: %s\n", id, strings.Join(keys, ", "))
	}
}

// newVerifyCommand re-hashes an installed module binary against its catalog
// digest pin — drift detection. Local-only: the re-hash never mutates state,
// so there is no daemon proxy (same posture as `module logs`).
func newVerifyCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "verify <module-id>",
		Short: "Re-hash the installed binary against its install-time digest record",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			res, err := mgr.Verify(args[0])
			if err != nil {
				// When verification ran far enough to compare digests, the
				// JSON result carries the evidence even on failure.
				if asJSON && res.Path != "" {
					printJSON(w, res)
				}
				fatal(err)
			}
			if asJSON {
				printJSON(w, res)
				return
			}
			fmt.Fprintf(w, "OK %s v%s — %s digest matches the install-time record\n  path: %s\n  %s: %s\n",
				res.Module, res.Version, res.Algorithm, res.Path, res.Algorithm, res.Got)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

// newCatalogCommand emits the embedded catalog in its canonical signed form
// — the exact bytes release signing covers. With --sign it also writes
// catalog.json.sig, signing with the MODULE_CATALOG_SIGNING_KEY env var
// (base64 Ed25519 seed) so the key never appears in argv or shell history.
func newCatalogCommand() *cobra.Command {
	var out, signEnv string
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Emit the embedded module catalog as canonical catalog.json",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			data, err := modules.MarshalCatalog()
			if err != nil {
				fatal(err)
			}
			if out == "" {
				fmt.Fprintln(w, string(data))
				return
			}
			if err := os.WriteFile(out, data, 0o600); err != nil {
				fatal(err)
			}
			fmt.Fprintf(w, "wrote %s (%d bytes)\n", out, len(data))
			if signEnv != "" {
				seed := os.Getenv(signEnv)
				if seed == "" {
					fatal(fmt.Errorf("env var %s is empty — cannot sign catalog", signEnv))
				}
				sig, err := modules.SignCatalog(data, seed)
				if err != nil {
					fatal(err)
				}
				sigPath := out + ".sig"
				if err := os.WriteFile(sigPath, []byte(sig+"\n"), 0o600); err != nil {
					fatal(err)
				}
				fmt.Fprintf(w, "wrote %s\n", sigPath)
			}
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Write catalog.json to this path instead of stdout")
	cmd.Flags().
		StringVar(&signEnv, "sign-with-env", "", "Also write <out>.sig signed with this env var's base64 Ed25519 seed")
	return cmd
}

// newCatalogKeygenCommand generates a fresh Ed25519 catalog signing keypair.
// Hidden: release infrastructure, not a user command.
func newCatalogKeygenCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "catalog-keygen",
		Short:  "Generate a module-catalog Ed25519 signing keypair",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			pub, seed, err := modules.GenerateCatalogKeypair()
			if err != nil {
				fatal(err)
			}
			fmt.Fprintf(w, "public:  %s\n", pub)
			fmt.Fprintf(w, "private: %s\n", seed)
			fmt.Fprintln(w, "\nBake the public key into pkg/sigverify (ReleasePubKeyB64)")
			fmt.Fprintln(w, "and store the private key as the MODULE_CATALOG_SIGNING_KEY GitHub secret.")
		},
	}
}

func newValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the modules section of config.json against the catalog",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			mgr, err := manager()
			if err != nil {
				fatal(err)
			}
			if err := modules.ValidateConfig(mgr.Config(), mgr.LookupSpec); err != nil {
				fatal(err)
			}
			fmt.Fprintln(w, "Module configuration OK")
		},
	}
}
