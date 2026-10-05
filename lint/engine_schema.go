package lint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
)

func prepareEngineSchema(root string, projectAware bool, config Config, compiled *compiledConfig, enabled []Rule) (*engineschema.Loaded, error) {
	if config.ExtensionAPI != nil {
		if !projectAware {
			return nil, failure.WrapPath(failure.ConfigInvalid, *config.ExtensionAPI,
				errors.New("extension_api requires a project-root-aware lint constructor"))
		}
		return loadEngineOverride(root, *config.ExtensionAPI, compiled)
	}
	if !rulesNeedEngineSchema(enabled) {
		return nil, nil
	}

	version := compiled.godotVersion
	loaded, err := engineschema.LoadEmbedded(version.Major, version.Minor)
	if err == nil {
		return loaded, nil
	}
	if errors.Is(err, engineschema.ErrUnsupportedVersion) {
		return nil, failure.Wrap(failure.ConfigInvalid, fmt.Errorf(
			"godot_version %q has no embedded engine schema; configure extension_api with a matching dump or use a gdkit release that supports Godot %d.%d: %w",
			config.GodotVersion, version.Major, version.Minor, err))
	}
	return nil, failure.Wrap(failure.ConfigInvalid, fmt.Errorf(
		"load embedded engine schema for godot_version %q: %w", config.GodotVersion, err))
}

func rulesNeedEngineSchema(rules []Rule) bool {
	for _, rule := range rules {
		if _, ok := rule.(EngineSchemaRule); ok {
			return true
		}
	}
	return false
}

func loadEngineOverride(root, configuredPath string, compiled *compiledConfig) (*engineschema.Loaded, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("resolve project root for extension_api %q: %w", configuredPath, err))
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("resolve project root for extension_api %q: %w", configuredPath, err))
	}
	target := filepath.Join(resolvedRoot, filepath.FromSlash(configuredPath))
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("resolve extension_api %q: %w", configuredPath, err))
	}
	inside, err := pathWithin(resolvedRoot, resolvedTarget)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigInvalid, configuredPath,
			fmt.Errorf("validate extension_api %q: %w", configuredPath, err))
	}
	if !inside {
		return nil, failure.WrapPath(failure.ConfigInvalid, configuredPath,
			fmt.Errorf("extension_api %q resolves outside the project root", configuredPath))
	}
	data, err := os.ReadFile(resolvedTarget)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("read extension_api %q: %w", configuredPath, err))
	}
	loaded, err := engineschema.LoadRaw(data, engineschema.SourceOverride, "")
	if err != nil {
		kind := failure.ConfigInvalid
		if errors.Is(err, engineschema.ErrRawParse) {
			kind = failure.ConfigParse
		}
		return nil, failure.WrapPath(kind, configuredPath,
			fmt.Errorf("load extension_api %q: %w", configuredPath, err))
	}
	if err := matchEngineVersion(compiled, loaded.Provenance.Version); err != nil {
		return nil, failure.WrapPath(failure.ConfigInvalid, configuredPath, err)
	}
	return loaded, nil
}

func pathWithin(root, target string) (bool, error) {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false, err
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func matchEngineVersion(config *compiledConfig, dump engineschema.Version) error {
	want := config.godotVersion
	if want.Major != dump.Major || want.Minor != dump.Minor || (config.godotVersionExactPatch && want.Patch != dump.Patch) {
		configured := fmt.Sprintf("%d.%d", want.Major, want.Minor)
		if config.godotVersionExactPatch {
			configured = want.String()
		}
		return fmt.Errorf("extension_api reports Godot %d.%d.%d, which does not match godot_version %s",
			dump.Major, dump.Minor, dump.Patch, configured)
	}
	return nil
}
