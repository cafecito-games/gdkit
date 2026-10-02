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
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true})
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
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true})
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
	reloaded, err := project.Load(project.Config{Root: root, HonorIgnoreFile: true})
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
