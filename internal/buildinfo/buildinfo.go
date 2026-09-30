// Package buildinfo exposes version-control metadata embedded in gdkit binaries.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

// These values are replaced by GoReleaser through -ldflags. Keep the fallback
// values useful for local builds and `go install`.
var (
	version   = "dev"
	commit    = ""
	date      = ""
	treeState = ""
)

// Info describes the source and toolchain used to build a gdkit binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
}

// Current returns linker-injected metadata, supplemented by Go's VCS build settings.
func Current() Info {
	info := Info{
		Version:   normalizeVersion(version),
		Commit:    commit,
		Date:      date,
		Dirty:     treeState == "dirty",
		GoVersion: runtime.Version(),
	}

	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
		info.Version = normalizeVersion(build.Main.Version)
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = setting.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = setting.Value
			}
		case "vcs.modified":
			if treeState == "" {
				info.Dirty, _ = strconv.ParseBool(setting.Value)
			}
		}
	}
	return info
}

// String returns a concise, human-readable version description.
func (i Info) String() string {
	result := "gdkit version " + i.Version
	if i.Commit != "" {
		result += " (commit " + shortCommit(i.Commit)
		if i.Dirty {
			result += ", dirty"
		}
		result += ")"
	} else if i.Dirty {
		result += " (dirty)"
	}
	if i.Date != "" {
		result += ", built " + i.Date
	}
	return result
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(value, "v")
}

func shortCommit(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
