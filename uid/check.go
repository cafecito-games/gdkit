package uid

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cafecito-games/gdkit/project"
)

// scriptExtension limits a run to GDScript. Godot also writes sidecars beside
// shaders and other text resources that carry no header to hold an identifier,
// but gdkit does not read those file types, so it does not speak for them.
const scriptExtension = ".gd"

// Check examines the identity of every script in the snapshot. It performs no
// I/O and changes nothing; Apply acts on the report it returns.
func Check(snapshot *project.Snapshot) Report {
	report := Report{Scripts: len(snapshot.Paths), Diagnostics: []Diagnostic{}}

	// Sidecars beside files other than scripts are skipped here, but their
	// identifiers still matter to Apply, which reserves them so a generated
	// one cannot collide with a shader's.
	owners := make(map[string]project.Sidecar, len(snapshot.Sidecars))
	claims := make(map[uint64][]string)
	for _, sidecar := range snapshot.Sidecars {
		if !strings.HasSuffix(sidecar.Owner, scriptExtension) {
			continue
		}
		owners[sidecar.Owner] = sidecar
		id, valid := Decode(sidecar.Text)
		if !valid {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule:    RuleMalformed,
				Message: fmt.Sprintf("%s does not hold a uid Godot could have written: %s", sidecar.Path, describe(sidecar.Text)),
				Path:    sidecar.Owner,
				UID:     sidecar.Text,
			})
			continue
		}
		claims[id] = append(claims[id], sidecar.Owner)
	}

	for _, path := range snapshot.Paths {
		if _, found := owners[path]; found {
			continue
		}
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Rule:    RuleMissing,
			Message: fmt.Sprintf("no %s.uid sidecar, so the script has no stable identity", path),
			Path:    path,
		})
	}

	for id, paths := range claims {
		if len(paths) < 2 {
			continue
		}
		// The first path in sorted order keeps the identifier and the rest
		// are reported, so both this report and any repair of it are the
		// same on every run.
		sort.Strings(paths)
		for _, path := range paths[1:] {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Rule:    RuleDuplicate,
				Message: fmt.Sprintf("%s is already claimed by %s", Encode(id), paths[0]),
				Path:    path,
				UID:     Encode(id),
			})
		}
	}

	report.sort()
	return report
}

// describe renders sidecar contents for a message, quoted and shortened, so a
// sidecar holding a whole file cannot flood the output.
func describe(text string) string {
	if text == "" {
		return "the file is empty"
	}
	const limit = 40
	if len(text) > limit {
		return fmt.Sprintf("%q...", text[:limit])
	}
	return fmt.Sprintf("%q", text)
}
