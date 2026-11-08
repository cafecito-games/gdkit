package licenseheader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/require"
)

func TestCheckAddsAndThenRecognizesManagedHeader(t *testing.T) {
	config := DefaultConfig()
	config.Header = []string{"Copyright (c) 2026 Cafecito Games", "", "SPDX-License-Identifier: MIT"}
	manager, err := New(config)
	require.NoError(t, err)
	snapshot := testSnapshot("player.gd", []byte("extends Node\n"))

	report := manager.Check(snapshot)
	require.True(t, report.HasChanges())
	require.False(t, report.HasDiagnostics())
	require.Equal(t, `# gdkit:license:begin
# Copyright (c) 2026 Cafecito Games
#
# SPDX-License-Identifier: MIT
# gdkit:license:end

extends Node
`, string(report.Results[0].Contents))

	snapshot.Scripts["player.gd"].Source = report.Results[0].Contents
	report = manager.Check(snapshot)
	require.False(t, report.HasChanges())
	require.Len(t, report.Results, 1)
	require.False(t, report.Results[0].Changed)
	require.Nil(t, report.Results[0].Contents)
}

func TestCheckUpdatesOnlyTheManagedBlock(t *testing.T) {
	config := DefaultConfig()
	config.Header = []string{"Copyright (c) 2027 New Holder", "SPDX-License-Identifier: Apache-2.0"}
	manager, err := New(config)
	require.NoError(t, err)
	source := `# gdkit:license:begin
# Copyright (c) 2026 Old Holder
# gdkit:license:end

## Player documentation.
extends Node
`
	report := manager.Check(testSnapshot("player.gd", []byte(source)))
	require.True(t, report.HasChanges())
	require.Equal(t, `# gdkit:license:begin
# Copyright (c) 2027 New Holder
# SPDX-License-Identifier: Apache-2.0
# gdkit:license:end

## Player documentation.
extends Node
`, string(report.Results[0].Contents))
}

func TestCheckPreservesBOMShebangLineEndingsAndExistingComments(t *testing.T) {
	config := DefaultConfig()
	config.Header = []string{"SPDX-License-Identifier: MIT"}
	manager, err := New(config)
	require.NoError(t, err)

	t.Run("bom and crlf", func(t *testing.T) {
		source := []byte("\xef\xbb\xbf## Existing docs.\r\nextends Node\r\n")
		report := manager.Check(testSnapshot("player.gd", source))
		require.Equal(t, "\xef\xbb\xbf# gdkit:license:begin\r\n# SPDX-License-Identifier: MIT\r\n# gdkit:license:end\r\n\r\n## Existing docs.\r\nextends Node\r\n", string(report.Results[0].Contents))

		snapshot := testSnapshot("player.gd", report.Results[0].Contents)
		require.False(t, manager.Check(snapshot).HasChanges())
	})

	t.Run("shebang", func(t *testing.T) {
		source := []byte("#!/usr/bin/env -S godot --headless --script\nextends SceneTree\n")
		report := manager.Check(testSnapshot("tool.gd", source))
		require.Equal(t, "#!/usr/bin/env -S godot --headless --script\n# gdkit:license:begin\n# SPDX-License-Identifier: MIT\n# gdkit:license:end\n\nextends SceneTree\n", string(report.Results[0].Contents))
	})
}

func TestCheckRefusesMalformedOrMisplacedMarkers(t *testing.T) {
	config := DefaultConfig()
	config.Header = []string{"SPDX-License-Identifier: MIT"}
	manager, err := New(config)
	require.NoError(t, err)
	cases := map[string]string{
		"missing end": `# gdkit:license:begin
# old
extends Node
`,
		"end first": `# gdkit:license:end
# gdkit:license:begin
extends Node
`,
		"repeated": `# gdkit:license:begin
# gdkit:license:end
# gdkit:license:begin
# gdkit:license:end
extends Node
`,
		"not a header": `## docs
# gdkit:license:begin
# old
# gdkit:license:end
extends Node
`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			report := manager.Check(testSnapshot("player.gd", []byte(source)))
			require.Empty(t, report.Results)
			require.Len(t, report.Diagnostics, 1)
			require.Equal(t, "license.marker", report.Diagnostics[0].Rule)
			require.False(t, report.HasChanges())
		})
	}
}

func TestEmptyHeaderIsAnInertDefault(t *testing.T) {
	manager, err := New(DefaultConfig())
	require.NoError(t, err)
	snapshot := testSnapshot("player.gd", []byte("# gdkit:license:begin\nextends Node\n"))
	report := manager.Check(snapshot)
	require.Len(t, report.Results, 1)
	require.False(t, report.HasChanges())
	require.False(t, report.HasDiagnostics())
}

func TestApplyWritesAtomicallyAndRefusesAConcurrentEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "player.gd")
	require.NoError(t, os.WriteFile(path, []byte("extends Node\n"), 0o640))
	config := DefaultConfig()
	config.Header = []string{"SPDX-License-Identifier: MIT"}
	manager, err := New(config)
	require.NoError(t, err)
	snapshot, err := project.Load(project.Config{Root: root, SourceRoots: []string{"."}})
	require.NoError(t, err)
	report := manager.Check(snapshot)

	written, err := Apply(snapshot, report)
	require.NoError(t, err)
	require.Equal(t, []string{"player.gd"}, written)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	require.NoError(t, os.WriteFile(path, []byte("extends Node2D\n"), 0o640))
	written, err = Apply(snapshot, report)
	require.ErrorContains(t, err, "file changed on disk")
	require.Empty(t, written)
}

func testSnapshot(path string, source []byte) *project.Snapshot {
	return &project.Snapshot{
		Paths:    []string{path},
		Selected: []string{path},
		Scripts: map[string]*project.Script{
			path: {Path: path, Source: source},
		},
	}
}
