package main

import "os"

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
