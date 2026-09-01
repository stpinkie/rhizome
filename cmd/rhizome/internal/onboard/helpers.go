package onboard

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	rhizome "github.com/stpinkie/rhizome"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/cliui"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/credential"
)

// embeddedFiles is the default workspace template embedded at the module root.
var embeddedFiles = rhizome.OnboardWorkspace

func runOnboard(cfg onboardConfig) {
	configPath := internal.GetConfigPath()

	configExists, err := configFileExists(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking config: %v\n", err)
		os.Exit(1)
	}

	var c *config.Config
	if configExists {
		c, err = config.LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading existing config: %v\n", err)
			os.Exit(1)
		}

		if !cfg.yes && !cfg.nonInteractive {
			fmt.Printf("Config already exists at %s\n", configPath)
			fmt.Print("Overwrite config with defaults? (y/n): ")
			var response string
			fmt.Scanln(&response)
			if strings.ToLower(strings.TrimSpace(response)) != "y" {
				fmt.Println("Aborted.")
				return
			}
		}

		if cfg.yes || !cfg.nonInteractive {
			// Preserve the previous workspace path by default so we don't
			// accidentally point a re-onboarded config at a fresh workspace.
			existingWorkspace := c.WorkspacePath()
			c = config.DefaultConfig()
			c.Agents.Defaults.Workspace = existingWorkspace
		}
	} else {
		c = config.DefaultConfig()
	}

	workspace, isNew, err := resolveWorkspace(c, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving workspace: %v\n", err)
		os.Exit(1)
	}

	if cfg.encrypt {
		if err := setupCredentialEncryption(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting up credential encryption: %v\n", err)
			os.Exit(1)
		}
	}

	if err := config.SaveConfig(configPath, c); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	if isNew {
		if err := copyEmbeddedToTarget(workspace); err != nil {
			fmt.Fprintf(os.Stderr, "Error copying workspace templates: %v\n", err)
			os.Exit(1)
		}
	}

	cliui.PrintOnboardComplete(internal.Logo, cfg.encrypt, configPath)
}

func configFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func resolveWorkspace(c *config.Config, cfg onboardConfig) (string, bool, error) {
	if cfg.workspace != "" {
		abs, err := filepath.Abs(cfg.workspace)
		if err != nil {
			return "", false, err
		}

		info, err := os.Stat(abs)
		if err == nil && info.IsDir() {
			c.Agents.Defaults.Workspace = abs
			return abs, false, nil
		}

		c.Agents.Defaults.Workspace = abs
		return abs, true, nil
	}

	if cfg.nonInteractive {
		ws := c.WorkspacePath()
		hasFiles, err := dirHasFiles(ws)
		if err != nil {
			return "", false, err
		}
		return ws, !hasFiles, nil
	}

	fmt.Print("Use an existing agent workspace? (y/n): ")
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(strings.TrimSpace(response)) == "y" {
		path, err := promptExistingWorkspacePath()
		if err != nil {
			return "", false, err
		}
		c.Agents.Defaults.Workspace = path
		return path, false, nil
	}

	ws := c.WorkspacePath()
	c.Agents.Defaults.Workspace = ws
	return ws, true, nil
}

func promptExistingWorkspacePath() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter the path to the existing workspace: ")
		text, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			fmt.Println("Path cannot be empty.")
			continue
		}

		abs, err := filepath.Abs(text)
		if err != nil {
			return "", err
		}

		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			fmt.Printf("Directory %q does not exist. Please try again.\n", abs)
			continue
		}

		return abs, nil
	}
}

func dirHasFiles(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func setupCredentialEncryption(cfg onboardConfig) error {
	var passphrase string
	var err error

	if cfg.nonInteractive {
		passphrase = os.Getenv(credential.PassphraseEnvVar)
		if passphrase == "" {
			return fmt.Errorf(
				"passphrase is required for --enc in non-interactive mode; set %s",
				credential.PassphraseEnvVar,
			)
		}
	} else {
		passphrase, err = promptPassphrase()
		if err != nil {
			return err
		}
	}

	keyPath, err := credential.DefaultSSHKeyPath()
	if err != nil {
		return fmt.Errorf("cannot determine SSH key path: %w", err)
	}

	if _, err := os.Stat(keyPath); err == nil {
		if cfg.nonInteractive {
			if !cfg.yes {
				return fmt.Errorf("SSH key already exists at %s; use --yes to overwrite", keyPath)
			}
		} else {
			fmt.Printf("\n⚠️  WARNING: %s already exists.\n", keyPath)
			fmt.Println("    Overwriting will invalidate any credentials previously encrypted with this key.")
			fmt.Print("    Overwrite? (y/n): ")
			var response string
			fmt.Scanln(&response)
			if strings.ToLower(strings.TrimSpace(response)) != "y" {
				fmt.Println("Keeping existing SSH key.")
				os.Setenv(credential.PassphraseEnvVar, passphrase)
				return nil
			}
		}
	}

	if err := credential.GenerateSSHKey(keyPath); err != nil {
		return fmt.Errorf("failed to generate SSH key: %w", err)
	}
	fmt.Printf("SSH key generated: %s\n", keyPath)

	// Set the passphrase in the current process so config.SaveConfig can encrypt
	// plaintext api_key entries with the new key.
	os.Setenv(credential.PassphraseEnvVar, passphrase)
	return nil
}

func promptPassphrase() (string, error) {
	fmt.Print("Enter passphrase for credential encryption: ")
	p1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading passphrase: %w", err)
	}
	if len(p1) == 0 {
		return "", fmt.Errorf("passphrase must not be empty")
	}

	fmt.Print("Confirm passphrase: ")
	p2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading passphrase confirmation: %w", err)
	}
	if string(p1) != string(p2) {
		return "", fmt.Errorf("passphrases do not match")
	}
	return string(p1), nil
}

func copyEmbeddedToTarget(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	return fs.WalkDir(embeddedFiles, "workspace", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel("workspace", path)
		if relErr != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, relErr)
		}
		if rel == "AGENTS.md" || rel == "IDENTITY.md" {
			return nil
		}

		targetPath := filepath.Join(targetDir, rel)
		if _, statErr := os.Stat(targetPath); statErr == nil {
			// Don't clobber an existing workspace.
			return nil
		}

		if mkErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkErr != nil {
			return fmt.Errorf("failed to create directory %s: %w", filepath.Dir(targetPath), mkErr)
		}

		data, readErr := embeddedFiles.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, readErr)
		}

		if writeErr := os.WriteFile(targetPath, data, 0o644); writeErr != nil {
			return fmt.Errorf("failed to write file %s: %w", targetPath, writeErr)
		}
		return nil
	})
}
