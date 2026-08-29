// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package config

import (
	"os"
	"path/filepath"

	"github.com/stpinkie/rhizome/pkg"
)

// Runtime environment variable keys for the rhizome process.
// These control the location of files and binaries at runtime and are read
// directly via os.Getenv / os.LookupEnv. All rhizome-specific keys use the
// RHIZOME_ prefix. Reference these constants instead of inline string
// literals to keep all supported knobs visible in one place and to prevent
// typos.
const (
	// EnvHome overrides the base directory for all rhizome data
	// (config, workspace, skills, auth store, …).
	// Default: ~/.rhizome
	EnvHome = "RHIZOME_HOME"

	// EnvConfig overrides the full path to the JSON config file.
	// Default: $RHIZOME_HOME/config.json
	EnvConfig = "RHIZOME_CONFIG"

	// EnvBuiltinSkills overrides the directory from which built-in
	// skills are loaded.
	// Default: <cwd>/skills
	EnvBuiltinSkills = "RHIZOME_BUILTIN_SKILLS"

	// EnvBinary overrides the path to the rhizome executable.
	// Used by the web launcher when spawning the gateway subprocess.
	// Default: resolved from the same directory as the current executable.
	EnvBinary = "RHIZOME_BINARY"

	// EnvGatewayHost overrides the host address for the gateway server.
	// Default: "localhost"
	EnvGatewayHost = "RHIZOME_GATEWAY_HOST"
)

func GetHome() string {
	homePath, _ := os.UserHomeDir()
	if rhizomeHome := os.Getenv(EnvHome); rhizomeHome != "" {
		homePath = rhizomeHome
	} else if homePath != "" {
		homePath = filepath.Join(homePath, pkg.DefaultRhizomeHome)
	}
	if homePath == "" {
		homePath = "."
	}
	return homePath
}
