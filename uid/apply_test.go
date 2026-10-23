package uid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyWritesASidecarGodotWouldHaveWritten(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{"player.gd": "extends Node\n"})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	require.Equal(t, []string{"player.gd.uid"}, written)
	// Godot writes the identifier with store_line, so the file is the text
	// and a single newline, with no BOM and nothing else.
	assert.Equal(t, "uid://b\n", readFile(t, root, "player.gd.uid"))
}

func TestApplyWritesASidecarPerScriptInPathOrder(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"world/enemy.gd": "extends Node\n",
		"player.gd":      "extends Node\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	want := []string{"player.gd.uid", "world/enemy.gd.uid"}
	require.Equal(t, want, written)
	assert.Equal(t, "uid://b\n", readFile(t, root, "player.gd.uid"))
	assert.Equal(t, "uid://c\n", readFile(t, root, "world/enemy.gd.uid"))
}

func TestApplyLeavesTheProjectCleanAfterwards(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":      "extends Node\n",
		"world/enemy.gd": "extends Node\n",
	})
	_, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	require.NoError(t, err)
	require.False(t, Check(reloaded).HasDiagnostics())
}

func TestApplyDoesNotTouchAnExistingSidecar(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bcd\n",
		"enemy.gd":      "extends Node\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	require.Equal(t, []string{"enemy.gd.uid"}, written)
	assert.Equal(t, "uid://bcd\n", readFile(t, root, "player.gd.uid"))
}

func TestApplyWithoutRepairLeavesMalformedAndDuplicateSidecars(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"c.gd":     "extends Node\n",
		"c.gd.uid": "nonsense\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	require.Len(t, written, 0)
	assert.Equal(t, "uid://bb\n", readFile(t, root, "b.gd.uid"))
	assert.Equal(t, "nonsense\n", readFile(t, root, "c.gd.uid"))
}

func TestApplyWithRepairReissuesTheSidecarsItCannotTrust(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"c.gd":     "extends Node\n",
		"c.gd.uid": "nonsense\n",
		"d.gd":     "extends Node\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(100), true)
	require.NoError(t, err)
	// The missing sidecar is created first, then the repairs in path order.
	want := []string{"d.gd.uid", "b.gd.uid", "c.gd.uid"}
	require.Equal(t, want, written)
	// a.gd is the first claimant, so it keeps the shared identifier.
	assert.Equal(t, "uid://bb\n", readFile(t, root, "a.gd.uid"))
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	require.NoError(t, err)
	require.False(t, Check(reloaded).HasDiagnostics())
}

// A repaired sidecar is renamed over, which must not widen its permissions.
func TestApplyKeepsThePermissionsOfARepairedSidecar(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "nonsense\n",
	})
	target := filepath.Join(root, "a.gd.uid")
	require.NoError(t, os.Chmod(target, 0o600))
	_, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	require.NoError(t, err)
	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// The generator must avoid identifiers already on disk, including those beside
// files Check does not speak for.
func TestApplyAvoidsIdentifiersAlreadyInTheProject(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":          "extends Node\n",
		"taken.gd":           "extends Node\n",
		"taken.gd.uid":       "uid://b\n",
		"water.gdshader.uid": "uid://c\n",
	})
	// The seeded generator would hand out uid://b and then uid://c, both of
	// which are taken, so player.gd must end up with uid://d.
	_, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	assert.Equal(t, "uid://d\n", readFile(t, root, "player.gd.uid"))
}

// Two scripts in one run cannot be handed the same identifier either.
func TestApplyDoesNotRepeatItself(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		files[name+".gd"] = "extends Node\n"
	}
	snapshot, root := loadProject(t, files)
	_, err := Apply(snapshot, Check(snapshot), NewGenerator(nil), false)
	require.NoError(t, err)
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	require.NoError(t, err)
	require.Len(t, reloaded.UIDs, 5)
}

// A sidecar created between the snapshot and the write is not clobbered.
func TestApplyRefusesToOverwriteASidecarThatAppeared(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{"player.gd": "extends Node\n"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "player.gd.uid"), []byte("uid://bcd\n"), 0o644))
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "player.gd.uid")
	assert.Len(t, written, 0)
	assert.Equal(t, "uid://bcd\n", readFile(t, root, "player.gd.uid"))
}

func TestApplyOnACleanProjectWritesNothing(t *testing.T) {
	snapshot, _ := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bcd\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	require.NoError(t, err)
	require.Len(t, written, 0)
}

func TestApplyRepointsABrokenReferenceAtThePathItNames(t *testing.T) {
	main := scene("uid://ddd", external("uid://xxx", "player.gd", "1_a"))
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bbb\n",
		"main.tscn":     main,
	})
	// Repointing a reference needs no --repair: the path beside it is the
	// authority, and it is what Godot already falls back to.
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	require.Equal(t, []string{"main.tscn"}, written)
	want := strings.Replace(main, "uid://xxx", "uid://bbb", 1)
	assert.Equal(t, want, readFile(t, root, "main.tscn"))
}

func TestApplyRepointsACrossedReference(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bbb\n",
		"enemy.gd":      "extends Node\n",
		"enemy.gd.uid":  "uid://ccc\n",
		"main.tscn":     scene("uid://ddd", external("uid://ccc", "player.gd", "1_a")),
	})
	report := Check(snapshot)
	require.Equal(t, []string{RuleCrossed}, rules(report))
	_, err := Apply(snapshot, report, seeded(1), false)
	require.NoError(t, err)
	assert.Contains(t, readFile(t, root, "main.tscn"), `uid="uid://bbb"`)
	assertClean(t, root)
}

// A script's load names no path, so nothing says what it meant and the
// reference is left exactly as it was.
func TestApplyLeavesAPathlessReferenceUntouched(t *testing.T) {
	source := "extends Node\n\nconst Scene := preload(\"uid://xyy\")\n"
	snapshot, root := loadProject(t, map[string]string{
		"loader.gd":     source,
		"loader.gd.uid": "uid://bbb\n",
	})
	report := Check(snapshot)
	written, err := Apply(snapshot, report, seeded(1), true)
	require.NoError(t, err)
	require.Len(t, written, 0)
	assert.Equal(t, source, readFile(t, root, "loader.gd"))
	assert.Len(t, report.Remaining(true), 1)
}

// Reissuing a declaration is only safe if the references to the old value move
// with it, which is the half --repair used to leave to the caller.
func TestApplyWithRepairMovesEveryReferenceToAReissuedIdentity(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"main.tscn":  scene("uid://b_local_reg_screen"),
		"other.tscn": scene("uid://ccc", "[ext_resource type=\"PackedScene\" uid=\"uid://b_local_reg_screen\" path=\"res://main.tscn\" id=\"1_a\"]"),
		"theme.tres": "[gd_resource type=\"Theme\" format=3 uid=\"uid://ddd\"]\n\n[ext_resource type=\"PackedScene\" uid=\"uid://b_local_reg_screen\" path=\"res://main.tscn\" id=\"1_b\"]\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	require.NoError(t, err)
	want := []string{"main.tscn", "other.tscn", "theme.tres"}
	require.Equal(t, want, written)
	for _, name := range want {
		contents := readFile(t, root, name)
		assert.NotContains(t, contents, "b_local_reg_screen", name)
		assert.Contains(t, contents, "uid://b", name)
	}
	assertClean(t, root)
}

// Without --repair the declaration stays, so the references to it must stay
// too: pointing them at a value that is about to change would be worse.
func TestApplyWithoutRepairLeavesAMalformedDeclarationAndItsReferences(t *testing.T) {
	other := scene("uid://ccc", "[ext_resource type=\"PackedScene\" uid=\"uid://b_local_reg_screen\" path=\"res://main.tscn\" id=\"1_a\"]")
	snapshot, root := loadProject(t, map[string]string{
		"main.tscn":  scene("uid://b_local_reg_screen"),
		"other.tscn": other,
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	require.NoError(t, err)
	require.Len(t, written, 0)
	assert.Equal(t, other, readFile(t, root, "other.tscn"))
}

// A reissued duplicate is a shared value, so only a reference that names the
// file by path can be attributed to it. Anything else keeps resolving to the
// first claimant, which is what it already did.
func TestApplyWithRepairMovesOnlyTheReferencesItCanAttribute(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "uid://bb\n",
		"b.gd":     "extends Node\n",
		"b.gd.uid": "uid://bb\n",
		"main.tscn": scene("uid://ddd",
			external("uid://bb", "b.gd", "1_a"),
			external("uid://bb", "a.gd", "2_b")),
	})
	_, err := Apply(snapshot, Check(snapshot), seeded(100), true)
	require.NoError(t, err)
	contents := readFile(t, root, "main.tscn")
	assert.Contains(t, contents, external(Encode(100), "b.gd", "1_a"))
	assert.Contains(t, contents, external("uid://bb", "a.gd", "2_b"))
	assertClean(t, root)
}

// Half a repair is worse than none: a run that cannot rewrite one reference
// writes nothing at all.
func TestApplyRefusesWhenAReferenceMovedUnderIt(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bbb\n",
		"main.tscn":     scene("uid://ddd", external("uid://xxx", "player.gd", "1_a")),
		"other.gd":      "extends Node\n",
	})
	edited := scene("uid://ddd", external("uid://xxy", "player.gd", "1_a"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.tscn"), []byte(edited), 0o644))
	written, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "main.tscn:3")
	assert.Len(t, written, 0)
	// The missing sidecar is part of the same run, so it is not written
	// either.
	_, statErr := os.Stat(filepath.Join(root, "other.gd.uid"))
	assert.Error(t, statErr)
}

// assertClean reloads the project and fails when anything is still wrong with
// its identities, which is the only real proof a repair finished.
func assertClean(t *testing.T, root string) {
	t.Helper()
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	require.NoError(t, err)
	require.False(t, Check(reloaded).HasDiagnostics())
}

// Creating a script's sidecar and leaving the scene that names it still
// pointing at nothing would need a second run to converge, so the reference
// adopts the identifier this run mints.
func TestApplyPointsAReferenceAtAnIdentityItCreates(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd": "extends Node\n",
		"main.tscn": scene("uid://ddd", external("uid://xxx", "player.gd", "1_a")),
	})
	report := Check(snapshot)
	require.Equal(t, []string{RuleDangling, RuleMissing}, rules(report))
	written, err := Apply(snapshot, report, seeded(1), false)
	require.NoError(t, err)
	require.Equal(t, []string{"player.gd.uid", "main.tscn"}, written)
	require.Equal(t, "uid://b\n", readFile(t, root, "player.gd.uid"))
	assert.Contains(t, readFile(t, root, "main.tscn"), external("uid://b", "player.gd", "1_a"))
	// One run is enough, which is the point.
	assertClean(t, root)
}

// Two files can hold the same malformed text, which makes the text useless for
// attribution — but an ext_resource still names one of them by path, and
// leaving that reference on the dead value would be half a repair.
func TestApplyWithRepairFollowsAPathEvenWhenTheOldTextIsShared(t *testing.T) {
	shared := "uid://b_local_reg_screen"
	snapshot, root := loadProject(t, map[string]string{
		"main.tscn": scene(shared),
		"alt.tscn":  scene(shared),
		"other.tscn": scene("uid://ccc",
			"[ext_resource type=\"PackedScene\" uid=\""+shared+"\" path=\"res://main.tscn\" id=\"1_a\"]"),
	})
	_, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	require.NoError(t, err)
	for _, name := range []string{"main.tscn", "alt.tscn", "other.tscn"} {
		assert.NotContains(t, readFile(t, root, name), shared, name)
	}
	assertClean(t, root)
}

// A reference that names no path cannot be attributed to either holder of a
// shared malformed value, so it is left alone rather than pointed at a guess.
func TestApplyWithRepairLeavesAnUnattributableReferenceAlone(t *testing.T) {
	shared := "uid://b_local_reg_screen"
	source := "extends Node\n\nconst Scene := preload(\"" + shared + "\")\n"
	snapshot, root := loadProject(t, map[string]string{
		"main.tscn":     scene(shared),
		"alt.tscn":      scene(shared),
		"loader.gd":     source,
		"loader.gd.uid": "uid://ccc\n",
	})
	_, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	require.NoError(t, err)
	assert.Equal(t, source, readFile(t, root, "loader.gd"))
}
