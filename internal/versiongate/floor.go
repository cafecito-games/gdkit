package versiongate

import (
	"fmt"
	"os"
)

// CheckFloor reports whether the version a binary reports about itself
// satisfies a config's declared minimum_gdkit_version. configPath names the
// config in the error, because the reader's next move is to open that file or
// to upgrade.
//
// reported is passed in rather than read here so each config package keeps
// its own override point: a test loads a config as an arbitrary release would
// by substituting that package's variable, and a shared one here would make
// every such test reach past its own package to do it.
//
// The development-build bypass is read from the environment, because a caller
// must be able to run this check at the exact point in its load order where
// the floor has to be reported — ahead of unknown-key rejection — and
// threading an environment read through that position would put the bypass in
// every caller's signature.
func CheckFloor(configPath, declared, reported string) error {
	if declared == "" {
		return nil
	}
	minimum, err := ParseRequirement(declared)
	if err != nil {
		return fmt.Errorf("minimum_gdkit_version %w", err)
	}
	if IsDevelopment(reported) {
		if os.Getenv(AllowDevelopmentEnvironmentVariable) != "" {
			return nil
		}
		return fmt.Errorf("%s requires gdkit %s or newer, which is not satisfied by a development build; set %s=1 to bypass",
			configPath, minimum, AllowDevelopmentEnvironmentVariable)
	}
	current, err := Parse(reported)
	if err != nil {
		return fmt.Errorf("%s requires gdkit %s or newer, but this binary reports an unrecognized version %q",
			configPath, minimum, reported)
	}
	if current.Less(minimum) {
		return fmt.Errorf("%s requires gdkit %s or newer, but this binary is %s", configPath, minimum, reported)
	}
	return nil
}
