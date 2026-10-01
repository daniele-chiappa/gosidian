package main

import (
	"log"
	"os"
)

// envOverride sets *target to the env var's value when the current value of
// target is still the default (i.e. the user didn't pass the corresponding
// CLI flag). This keeps the POSIX precedence CLI > env > default.
func envOverride(target *string, envName, defaultVal string) {
	if target == nil || *target != defaultVal {
		return
	}
	if v := os.Getenv(envName); v != "" {
		*target = v
	}
}

// cliVaultDir returns the vault of a CLI subcommand: --vault, or else
// GOSIDIAN_VAULT, as for `serve`. Inside the container the variable is set
// and the subcommands used to demand the flag anyway (IMP-112).
func cliVaultDir(flagVal string) string {
	envOverride(&flagVal, "GOSIDIAN_VAULT", "")
	if flagVal == "" {
		log.Fatal("--vault (or GOSIDIAN_VAULT) is required")
	}
	return flagVal
}
