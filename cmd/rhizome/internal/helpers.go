package internal

import (
	"os"
	"path/filepath"

	"github.com/stpinkie/rhizome/pkg"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
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
