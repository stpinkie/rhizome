package skills

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/sigverify"
	"github.com/stpinkie/rhizome/pkg/skills"
)

// newIndexCommand emits the embedded curated skills index in its canonical
// signed form — the exact bytes release signing covers. With
// --sign-with-env it also writes index.json.sig, signing with the named env
// var's base64 Ed25519 seed (MODULE_CATALOG_SIGNING_KEY during release) so
// the key never appears in argv or shell history. Mirrors
// `rhizome module catalog`.
func newIndexCommand() *cobra.Command {
	var out, signEnv string
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Emit the embedded curated skills index as canonical index.json",
		Args:  cobra.NoArgs,
		// Daemonless emit used by release signing — shadow the parent's
		// config load (a release runner has no config.json).
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			data, err := skills.MarshalCuratedIndex()
			if err != nil {
				return err
			}
			if out == "" {
				fmt.Fprintln(w, string(data))
				return nil
			}
			if err := os.WriteFile(out, data, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(w, "wrote %s (%d bytes)\n", out, len(data))
			if signEnv == "" {
				return nil
			}
			seed := os.Getenv(signEnv)
			if seed == "" {
				return fmt.Errorf("env var %s is empty — cannot sign index", signEnv)
			}
			sig, err := sigverify.SignRelease(data, seed)
			if err != nil {
				return err
			}
			sigPath := out + ".sig"
			if err := os.WriteFile(sigPath, []byte(sig+"\n"), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(w, "wrote %s\n", sigPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "Write index.json to this path instead of stdout")
	cmd.Flags().
		StringVar(&signEnv, "sign-with-env", "", "Also write <out>.sig signed with this env var's base64 Ed25519 seed")
	return cmd
}
