package lint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
)

func prepareEngineSchema(root string, projectAware bool, config Config, compiled *compiledConfig, enabled []Rule) (*engineschema.Loaded, error) {
	return prepareEngineSchemaWithLoader(root, projectAware, config, compiled, enabled, engineschema.LoadEmbedded)
}

type embeddedSchemaLoader func(int, int) (*engineschema.Loaded, error)

func prepareEngineSchemaWithLoader(root string, projectAware bool, config Config, compiled *compiledConfig, enabled []Rule, loadEmbedded embeddedSchemaLoader) (*engineschema.Loaded, error) {
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
	loaded, err := loadEmbedded(version.Major, version.Minor)
	if err == nil {
		return loaded, nil
	}
	if errors.Is(err, engineschema.ErrUnsupportedVersion) {
		return nil, failure.Wrap(failure.ConfigInvalid, fmt.Errorf(
			"godot_version %q has no embedded engine schema; configure extension_api with a matching dump or use a gdkit release that supports Godot %d.%d: %w",
			config.GodotVersion, version.Major, version.Minor, err))
	}
	return nil, failure.Wrap(failure.AnalysisFailed, fmt.Errorf(
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
	rootHandle, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("open project root for extension_api %q: %w", configuredPath, err))
	}
	file, openErr := rootHandle.OpenFile(filepath.FromSlash(configuredPath), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	rootCloseErr := rootHandle.Close()
	if openErr != nil {
		if currentTarget, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
			if currentlyInside, withinErr := pathWithin(resolvedRoot, currentTarget); withinErr == nil && !currentlyInside {
				return nil, failure.WrapPath(failure.ConfigInvalid, configuredPath,
					fmt.Errorf("extension_api %q resolves outside the project root", configuredPath))
			}
		}
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("open extension_api %q inside project root: %w", configuredPath, openErr))
	}
	if rootCloseErr != nil {
		_ = file.Close()
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("close project root for extension_api %q: %w", configuredPath, rootCloseErr))
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("inspect open extension_api %q: %w", configuredPath, statErr))
	}
	if !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, failure.WrapPath(failure.ConfigInvalid, configuredPath,
			fmt.Errorf("extension_api %q is not a regular file", configuredPath))
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("read extension_api %q: %w", configuredPath, readErr))
	}
	if closeErr != nil {
		return nil, failure.WrapPath(failure.ConfigRead, configuredPath,
			fmt.Errorf("close extension_api %q: %w", configuredPath, closeErr))
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
