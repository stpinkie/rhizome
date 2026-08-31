package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"

	"github.com/stpinkie/rhizome/pkg"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

const Logo = pkg.Logo

// GetRhizomeHome returns the rhizome home directory.
// Priority: $RHIZOME_HOME > ~/.rhizome
func GetRhizomeHome() string {
	return config.GetHome()
}

func GetConfigPath() string {
	if configPath := os.Getenv(config.EnvConfig); configPath != "" {
		return configPath
	}
	return filepath.Join(GetRhizomeHome(), "config.json")
}

func LoadConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig(GetConfigPath())
	if err != nil {
		return nil, err
	}
	logger.SetLevelFromString(cfg.Gateway.LogLevel)
	return cfg, nil
}

// FormatVersion returns the version string with optional git commit
// Deprecated: Use pkg/config.FormatVersion instead
func FormatVersion() string {
	return config.FormatVersion()
}

// FormatBuildInfo returns build time and go version info
// Deprecated: Use pkg/config.FormatBuildInfo instead
func FormatBuildInfo() (string, string) {
	return config.FormatBuildInfo()
}

// GetVersion returns the version string
// Deprecated: Use pkg/config.GetVersion instead
func GetVersion() string {
	return config.GetVersion()
}

// LoadIdentity loads the node identity from identityDir. If the identity is
// encrypted, it first tries the OS keyring, then $RHIZOME_IDENTITY_PASSPHRASE,
// and finally prompts for a passphrase on a TTY.
func LoadIdentity(identityDir string) (*identity.Derived, string, error) {
	d, name, err := identity.Load(identityDir)
	if err == nil {
		return d, name, nil
	}
	if !errors.Is(err, identity.ErrIdentityEncrypted) {
		return nil, "", err
	}

	provider := identity.KeyringProvider{}
	d, name, err = identity.LoadWithProvider(identityDir, &provider)
	if err == nil {
		return d, name, nil
	}

	passphrase := os.Getenv("RHIZOME_IDENTITY_PASSPHRASE")
	if passphrase == "" && term.IsTerminal(os.Stdin.Fd()) {
		fmt.Print("Identity passphrase: ")
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Println()
		if err != nil {
			return nil, "", fmt.Errorf("read passphrase: %w", err)
		}
		passphrase = string(b)
	}
	if passphrase == "" {
		return nil, "", fmt.Errorf("encrypted identity: set RHIZOME_IDENTITY_PASSPHRASE or provide a passphrase")
	}

	return identity.LoadWithProvider(identityDir, &identity.ScryptProvider{Passphrase: passphrase})
}
