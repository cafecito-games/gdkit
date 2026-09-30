package buildinfo

import "testing"

func TestCurrentUsesInjectedValues(t *testing.T) {
	oldVersion, oldCommit, oldDate, oldTreeState := version, commit, date, treeState
	t.Cleanup(func() {
		version, commit, date, treeState = oldVersion, oldCommit, oldDate, oldTreeState
	})
	version = "v1.2.3"
	commit = "0123456789abcdef0123456789abcdef01234567"
	date = "2026-09-30T12:00:00Z"
	treeState = "dirty"

	got := Current()
	if got.Version != "1.2.3" || got.Commit != commit || got.Date != date || !got.Dirty {
		t.Fatalf("Current() = %#v", got)
	}
}

func TestInfoString(t *testing.T) {
	info := Info{
		Version: "1.2.3",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		Date:    "2026-09-30T12:00:00Z",
		Dirty:   true,
	}
	want := "gdkit version 1.2.3 (commit 0123456789ab, dirty), built 2026-09-30T12:00:00Z"
	if got := info.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestNormalizeVersion(t *testing.T) {
	for input, want := range map[string]string{
		"v1.2.3":  "1.2.3",
		"1.2.3":   "1.2.3",
		"(devel)": "dev",
		"":        "dev",
	} {
		if got := normalizeVersion(input); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", input, got, want)
		}
	}
}
