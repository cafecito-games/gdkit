package architecture

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/internal/versiongate"
)

// pretendVersion makes LoadConfig see a fixed binary version for one test.
func pretendVersion(t *testing.T, value string) {
	t.Helper()
	previous := detectedVersion
	t.Cleanup(func() { detectedVersion = previous })
	detectedVersion = func() string { return value }
}

func TestLoadConfigAcceptsASatisfiedMinimumVersion(t *testing.T) {
	for _, binary := range []string{"0.3.0", "0.3.1", "0.4.0", "1.0.0", "0.3.0-SNAPSHOT-9060007"} {
		t.Run(binary, func(t *testing.T) {
			pretendVersion(t, binary)
			root := t.TempDir()
			writeProject(t, root, map[string]string{
				DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
			})
			config, err := LoadConfig(root, "")
			if err != nil {
				t.Fatalf("binary %s did not satisfy floor 0.3.0: %v", binary, err)
			}
			if config.MinimumGDKitVersion != "0.3.0" {
				t.Fatalf("MinimumGDKitVersion = %q, want \"0.3.0\"", config.MinimumGDKitVersion)
			}
		})
	}
}

func TestLoadConfigRejectsABinaryBelowTheMinimumVersion(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, "")
	if err == nil {
		t.Fatal("a binary below the floor was accepted")
	}
	for _, want := range []string{"0.3.0", "0.2.0", DefaultConfigPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// The floor must be read before unknown-key reporting. A config written for a
// newer gdkit uses both the new field and the new syntax, and naming the
// unknown key would point the reader at the wrong problem.
func TestLoadConfigReportsTheMinimumVersionBeforeAnUnknownKey(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0", "zones": ["anything"]}`,
	})
	_, err := LoadConfig(root, "")
	if err == nil {
		t.Fatal("a binary below the floor was accepted")
	}
	if !strings.Contains(err.Error(), "0.3.0") {
		t.Fatalf("error %q does not report the required version", err)
	}
	if strings.Contains(err.Error(), "zones") {
		t.Fatalf("error %q names the unknown key instead of the version floor", err)
	}
}

func TestLoadConfigReportsTheMinimumVersionBeforeAValidationError(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0", "classifications": [{"pattern": "aaa/**", "feature": "f"}]}`,
	})
	_, err := LoadConfig(root, "")
	if err == nil {
		t.Fatal("a binary below the floor was accepted")
	}
	if !strings.Contains(err.Error(), "0.3.0") {
		t.Fatalf("error %q does not report the required version", err)
	}
	if strings.Contains(err.Error(), "requires a layer") {
		t.Fatalf("error %q reports the validation failure instead of the version floor", err)
	}
}

func TestLoadConfigRejectsAMalformedMinimumVersion(t *testing.T) {
	for _, value := range []string{"0.3", "v0.3.0", "dev", "latest", "0.3.0-rc.1"} {
		t.Run(value, func(t *testing.T) {
			pretendVersion(t, "0.2.0")
			root := t.TempDir()
			writeProject(t, root, map[string]string{
				DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "` + value + `"}`,
			})
			_, err := LoadConfig(root, "")
			if err == nil {
				t.Fatalf("minimum_gdkit_version %q was accepted", value)
			}
			if !strings.Contains(err.Error(), "minimum_gdkit_version") {
				t.Fatalf("error %q does not name the offending key", err)
			}
			if !strings.Contains(err.Error(), "major.minor.patch") {
				t.Fatalf("error %q does not say what a valid value looks like", err)
			}
		})
	}
}

func TestLoadConfigIgnoresAnAbsentMinimumVersion(t *testing.T) {
	pretendVersion(t, "0.0.1")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1}`,
	})
	config, err := LoadConfig(root, "")
	if err != nil {
		t.Fatalf("a config without a floor was rejected: %v", err)
	}
	if config.MinimumGDKitVersion != "" {
		t.Fatalf("MinimumGDKitVersion = %q, want empty", config.MinimumGDKitVersion)
	}
}

func TestLoadConfigFailsClosedOnADevelopmentBuild(t *testing.T) {
	pretendVersion(t, "dev")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, "")
	if err == nil {
		t.Fatal("a development build satisfied a version floor")
	}
	if !strings.Contains(err.Error(), versiongate.AllowDevelopmentEnvironmentVariable) {
		t.Fatalf("error %q does not name the bypass", err)
	}

	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	if _, err := LoadConfig(root, ""); err != nil {
		t.Fatalf("%s did not bypass the floor: %v", versiongate.AllowDevelopmentEnvironmentVariable, err)
	}
}

// The bypass covers development builds only; it must not excuse a real
// version that is below the floor.
func TestLoadConfigBypassDoesNotExcuseAnOldRelease(t *testing.T) {
	pretendVersion(t, "0.2.0")
	t.Setenv(versiongate.AllowDevelopmentEnvironmentVariable, "1")
	root := t.TempDir()
	writeProject(t, root, map[string]string{
		DefaultConfigPath: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	if _, err := LoadConfig(root, ""); err == nil {
		t.Fatal("the development bypass excused an old release")
	}
}

func TestLoadConfigNamesTheResolvedConfigPath(t *testing.T) {
	pretendVersion(t, "0.2.0")
	root := t.TempDir()
	name := filepath.ToSlash(filepath.Join("config", "strict.json"))
	writeProject(t, root, map[string]string{
		name: `{"version": 1, "minimum_gdkit_version": "0.3.0"}`,
	})
	_, err := LoadConfig(root, name)
	if err == nil {
		t.Fatal("a binary below the floor was accepted")
	}
	if !strings.Contains(filepath.ToSlash(err.Error()), "strict.json") {
		t.Fatalf("error %q does not name the resolved config path", err)
	}
}

// A generated config must not pin itself to whichever binary generated it.
func TestDefaultConfigDeclaresNoMinimumVersion(t *testing.T) {
	if got := DefaultConfig().MinimumGDKitVersion; got != "" {
		t.Fatalf("DefaultConfig().MinimumGDKitVersion = %q, want empty", got)
	}
}
