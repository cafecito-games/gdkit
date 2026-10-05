package uid

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cafecito-games/gdkit/project"
)

func TestApplyWritesASidecarGodotWouldHaveWritten(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{"player.gd": "extends Node\n"})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"player.gd.uid"}) {
		t.Fatalf("written = %v, want player.gd.uid", written)
	}
	// Godot writes the identifier with store_line, so the file is the text
	// and a single newline, with no BOM and nothing else.
	if got := readFile(t, root, "player.gd.uid"); got != "uid://b\n" {
		t.Errorf("sidecar = %q, want %q", got, "uid://b\n")
	}
}

func TestApplyWritesASidecarPerScriptInPathOrder(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"world/enemy.gd": "extends Node\n",
		"player.gd":      "extends Node\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"player.gd.uid", "world/enemy.gd.uid"}
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("written = %v, want %v", written, want)
	}
	if got := readFile(t, root, "player.gd.uid"); got != "uid://b\n" {
		t.Errorf("player sidecar = %q, want uid://b", got)
	}
	if got := readFile(t, root, "world/enemy.gd.uid"); got != "uid://c\n" {
		t.Errorf("enemy sidecar = %q, want uid://c", got)
	}
}

func TestApplyLeavesTheProjectCleanAfterwards(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":      "extends Node\n",
		"world/enemy.gd": "extends Node\n",
	})
	if _, err := Apply(snapshot, Check(snapshot), seeded(1), false); err != nil {
		t.Fatal(err)
	}
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if report := Check(reloaded); report.HasDiagnostics() {
		t.Fatalf("diagnostics after writing = %v, want none", report.Diagnostics)
	}
}

func TestApplyDoesNotTouchAnExistingSidecar(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bcd\n",
		"enemy.gd":      "extends Node\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"enemy.gd.uid"}) {
		t.Fatalf("written = %v, want enemy.gd.uid only", written)
	}
	if got := readFile(t, root, "player.gd.uid"); got != "uid://bcd\n" {
		t.Errorf("player sidecar = %q, want it untouched", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want nothing", written)
	}
	if got := readFile(t, root, "b.gd.uid"); got != "uid://bb\n" {
		t.Errorf("duplicate sidecar = %q, want it untouched", got)
	}
	if got := readFile(t, root, "c.gd.uid"); got != "nonsense\n" {
		t.Errorf("malformed sidecar = %q, want it untouched", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	// The missing sidecar is created first, then the repairs in path order.
	want := []string{"d.gd.uid", "b.gd.uid", "c.gd.uid"}
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("written = %v, want %v", written, want)
	}
	// a.gd is the first claimant, so it keeps the shared identifier.
	if got := readFile(t, root, "a.gd.uid"); got != "uid://bb\n" {
		t.Errorf("first claimant = %q, want it untouched", got)
	}
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if report := Check(reloaded); report.HasDiagnostics() {
		t.Fatalf("diagnostics after repair = %v, want none", report.Diagnostics)
	}
}

// A repaired sidecar is renamed over, which must not widen its permissions.
func TestApplyKeepsThePermissionsOfARepairedSidecar(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{
		"a.gd":     "extends Node\n",
		"a.gd.uid": "nonsense\n",
	})
	target := filepath.Join(root, "a.gd.uid")
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(snapshot, Check(snapshot), seeded(1), true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("permissions = %o, want 600", got)
	}
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
	if _, err := Apply(snapshot, Check(snapshot), seeded(1), false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, root, "player.gd.uid"); got != "uid://d\n" {
		t.Errorf("sidecar = %q, want uid://d", got)
	}
}

// Two scripts in one run cannot be handed the same identifier either.
func TestApplyDoesNotRepeatItself(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		files[name+".gd"] = "extends Node\n"
	}
	snapshot, root := loadProject(t, files)
	if _, err := Apply(snapshot, Check(snapshot), NewGenerator(nil), false); err != nil {
		t.Fatal(err)
	}
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.UIDs) != 5 {
		t.Fatalf("project holds %d distinct identifiers, want 5", len(reloaded.UIDs))
	}
}

// A sidecar created between the snapshot and the write is not clobbered.
func TestApplyRefusesToOverwriteASidecarThatAppeared(t *testing.T) {
	snapshot, root := loadProject(t, map[string]string{"player.gd": "extends Node\n"})
	if err := os.WriteFile(filepath.Join(root, "player.gd.uid"), []byte("uid://bcd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := Apply(snapshot, Check(snapshot), seeded(1), false)
	if err == nil {
		t.Fatal("Apply succeeded, want an error naming the sidecar")
	}
	if !strings.Contains(err.Error(), "player.gd.uid") {
		t.Errorf("error = %v, want it to name player.gd.uid", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want nothing", written)
	}
	if got := readFile(t, root, "player.gd.uid"); got != "uid://bcd\n" {
		t.Errorf("sidecar = %q, want it untouched", got)
	}
}

func TestApplyOnACleanProjectWritesNothing(t *testing.T) {
	snapshot, _ := loadProject(t, map[string]string{
		"player.gd":     "extends Node\n",
		"player.gd.uid": "uid://bcd\n",
	})
	written, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want nothing", written)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"main.tscn"}) {
		t.Fatalf("written = %v, want main.tscn", written)
	}
	want := strings.Replace(main, "uid://xxx", "uid://bbb", 1)
	if got := readFile(t, root, "main.tscn"); got != want {
		t.Errorf("main.tscn = %q, want only the identifier changed", got)
	}
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
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleCrossed}) {
		t.Fatalf("rules = %v, want one %s", got, RuleCrossed)
	}
	if _, err := Apply(snapshot, report, seeded(1), false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, root, "main.tscn"); !strings.Contains(got, `uid="uid://bbb"`) {
		t.Errorf("main.tscn = %q, want the reference pointing at player.gd", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want nothing", written)
	}
	if got := readFile(t, root, "loader.gd"); got != source {
		t.Errorf("loader.gd = %q, want it untouched", got)
	}
	if got := len(report.Remaining(true)); got != 1 {
		t.Errorf("Remaining(true) has %d diagnostics, want the preload left behind", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"main.tscn", "other.tscn", "theme.tres"}
	if !reflect.DeepEqual(written, want) {
		t.Fatalf("written = %v, want %v", written, want)
	}
	for _, name := range want {
		contents := readFile(t, root, name)
		if strings.Contains(contents, "b_local_reg_screen") {
			t.Errorf("%s = %q, want the old value gone", name, contents)
		}
		if !strings.Contains(contents, "uid://b") {
			t.Errorf("%s = %q, want the minted identifier", name, contents)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want nothing", written)
	}
	if got := readFile(t, root, "other.tscn"); got != other {
		t.Errorf("other.tscn = %q, want it untouched", got)
	}
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
	if _, err := Apply(snapshot, Check(snapshot), seeded(100), true); err != nil {
		t.Fatal(err)
	}
	contents := readFile(t, root, "main.tscn")
	if !strings.Contains(contents, external(Encode(100), "b.gd", "1_a")) {
		t.Errorf("main.tscn = %q, want b.gd's reference moved to the new identifier", contents)
	}
	if !strings.Contains(contents, external("uid://bb", "a.gd", "2_b")) {
		t.Errorf("main.tscn = %q, want a.gd's reference left on the shared value", contents)
	}
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
	if err := os.WriteFile(filepath.Join(root, "main.tscn"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := Apply(snapshot, Check(snapshot), seeded(1), true)
	if err == nil {
		t.Fatal("Apply succeeded, want it to refuse")
	}
	if !strings.Contains(err.Error(), "main.tscn:3") {
		t.Errorf("error = %v, want it to name main.tscn:3", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want nothing", written)
	}
	// The missing sidecar is part of the same run, so it is not written
	// either.
	if _, statErr := os.Stat(filepath.Join(root, "other.gd.uid")); statErr == nil {
		t.Error("other.gd.uid was written, want the whole run abandoned")
	}
}

// assertClean reloads the project and fails when anything is still wrong with
// its identities, which is the only real proof a repair finished.
func assertClean(t *testing.T, root string) {
	t.Helper()
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true, Identities: true})
	if err != nil {
		t.Fatal(err)
	}
	if report := Check(reloaded); report.HasDiagnostics() {
		t.Fatalf("diagnostics after writing = %v, want none", report.Diagnostics)
	}
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
	if got := rules(report); !reflect.DeepEqual(got, []string{RuleDangling, RuleMissing}) {
		t.Fatalf("rules = %v, want the scene's reference and the missing sidecar", got)
	}
	written, err := Apply(snapshot, report, seeded(1), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, []string{"player.gd.uid", "main.tscn"}) {
		t.Fatalf("written = %v, want the sidecar then the scene", written)
	}
	if got := readFile(t, root, "player.gd.uid"); got != "uid://b\n" {
		t.Fatalf("sidecar = %q, want uid://b", got)
	}
	if got := readFile(t, root, "main.tscn"); !strings.Contains(got, external("uid://b", "player.gd", "1_a")) {
		t.Errorf("main.tscn = %q, want the reference pointing at the new identifier", got)
	}
	// One run is enough, which is the point.
	assertClean(t, root)
}
