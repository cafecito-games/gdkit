package ignore

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, source string) *Matcher {
	t.Helper()
	matcher, err := Parse([]byte(source))
	if err != nil {
		t.Fatalf("Parse(%q): %v", source, err)
	}
	return matcher
}

func TestIgnored(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		path        string
		isDirectory bool
		want        bool
	}{
		{"empty file ignores nothing", "", "a.gd", false, false},
		{"blank lines are skipped", "\n\n  \n", "a.gd", false, false},
		{"comment is skipped", "# a.gd\n", "a.gd", false, false},
		{"comment does not hide later patterns", "# note\na.gd\n", "a.gd", false, true},
		{"no inline comments", "a.gd # note\n", "a.gd", false, false},
		{"inline comment text is part of the pattern", "a.gd # note\n", "a.gd # note", false, true},
		{"escaped hash is literal", "\\#a.gd\n", "#a.gd", false, true},
		{"trailing spaces are trimmed", "a.gd   \n", "a.gd", false, true},
		{"escaped trailing space is kept", "a.gd\\ \n", "a.gd ", false, true},
		{"escaped trailing space does not match the bare name", "a.gd\\ \n", "a.gd", false, false},
		{"interior spaces are kept", "my file.gd\n", "my file.gd", false, true},
		{"crlf line endings", "a.gd\r\nb.gd\r\n", "a.gd", false, true},
		{"crlf on the last pattern", "a.gd\r\nb.gd\r\n", "b.gd", false, true},
		{"byte order mark", "\ufeffa.gd\n", "a.gd", false, true},
		{"no trailing newline", "a.gd", "a.gd", false, true},
		{"letter case is significant", "Player.gd\n", "player.gd", false, false},
		{"question mark matches one multi-byte character", "?.gd\n", "é.gd", false, true},
		{"question mark does not match one byte of a character", "??.gd\n", "é.gd", false, false},
		{"class matches one multi-byte character", "[é].gd\n", "é.gd", false, true},

		{"name matches at the root", "a.gd\n", "a.gd", false, true},
		{"name matches at any depth", "a.gd\n", "x/y/a.gd", false, true},
		{"name does not match a longer name", "a.gd\n", "aa.gd", false, false},
		{"name matches a directory", "build\n", "x/build", true, true},
		{"file inside a matched directory", "build\n", "x/build/a.gd", false, true},
		{"extension glob matches anywhere", "*.pb.gd\n", "client/protocol/messages.pb.gd", false, true},
		{"extension glob does not match another suffix", "*.pb.gd\n", "client/protocol/messages.gd", false, false},

		{"directory pattern matches a root directory", "addons/\n", "addons", true, true},
		{"directory pattern matches a nested directory", "addons/\n", "apps/editor/addons", true, true},
		{"directory pattern covers nested files", "addons/\n", "apps/editor/addons/plugin/a.gd", false, true},
		{"directory pattern does not match a file of that name", "addons/\n", "addons", false, false},
		{"directory pattern does not match a nested file of that name", "addons/\n", "x/addons", false, false},
		{"directory pattern does not match a longer name", "addons/\n", "my_addons/a.gd", false, false},

		{"leading slash anchors to the root", "/client/protocol/\n", "client/protocol/a.gd", false, true},
		{"leading slash does not match deeper", "/client/protocol/\n", "apps/client/protocol/a.gd", false, false},
		{"leading slash anchors a single name", "/a.gd\n", "a.gd", false, true},
		{"anchored single name does not match deeper", "/a.gd\n", "x/a.gd", false, false},
		{"interior slash anchors to the root", "client/protocol/*.gd\n", "client/protocol/a.gd", false, true},
		{"interior slash does not match deeper", "client/protocol/*.gd\n", "apps/client/protocol/a.gd", false, false},
		{"anchored star stays in its directory", "client/protocol/*.gd\n", "client/protocol/sub/a.gd", false, false},

		{"star does not cross a slash", "client/*.gd\n", "client/x/a.gd", false, false},
		{"star matches an empty run", "a*.gd\n", "a.gd", false, true},
		{"question mark matches one character", "a?.gd\n", "ab.gd", false, true},
		{"question mark needs a character", "a?.gd\n", "a.gd", false, false},
		{"question mark does not match a slash", "client/a?b.gd\n", "client/a/b.gd", false, false},
		{"character set", "[abc].gd\n", "b.gd", false, true},
		{"character set miss", "[abc].gd\n", "d.gd", false, false},
		{"character range", "[a-c]1.gd\n", "x/c1.gd", false, true},
		{"character range miss", "[a-c]1.gd\n", "d1.gd", false, false},
		{"negated character class", "[!a].gd\n", "b.gd", false, true},
		{"negated character class miss", "[!a].gd\n", "a.gd", false, false},
		{"negated character class does not match a slash", "x[!a]y.gd\n", "x/y.gd", false, false},
		{"closing bracket first is literal", "[]a].gd\n", "].gd", false, true},
		{"regular expression characters are literal", "a+(b).gd\n", "a+(b).gd", false, true},
		{"dot is literal", "a.gd\n", "axgd", false, false},

		{"leading double star matches at the root", "**/generated\n", "generated", true, true},
		{"leading double star matches in any directory", "**/generated/a.gd\n", "x/y/generated/a.gd", false, true},
		{"trailing double star matches everything inside", "generated/**\n", "generated/x/y/a.gd", false, true},
		{"trailing double star does not match the directory", "generated/**\n", "generated", true, false},
		{"trailing double star is anchored", "generated/**\n", "x/generated/a.gd", false, false},
		{"interior double star matches zero directories", "a/**/b.gd\n", "a/b.gd", false, true},
		{"interior double star matches several directories", "a/**/b.gd\n", "a/x/y/b.gd", false, true},
		{"interior double star keeps its ends", "a/**/b.gd\n", "x/a/b.gd", false, false},
		{"double star inside a name is a single star", "a**b.gd\n", "x/aXXb.gd", false, true},
		{"double star inside a name does not cross a slash", "x/a**b.gd\n", "x/a/b.gd", false, false},
		{"three stars are a double star", "a/***/b.gd\n", "a/x/y/b.gd", false, true},
		{"three leading stars match at the root", "***/b.gd\n", "b.gd", false, true},
		{"double star after a literal prefix crosses directories", "a**/b.gd\n", "a/x/b.gd", false, true},
		{"double star after a literal prefix matches nothing at all", "a**/b.gd\n", "ab.gd", false, true},
		{"double star after a literal prefix extends the name", "a**/b.gd\n", "ax/y/b.gd", false, true},
		{"double star after a wildcard stays in its segment", "?a**/b.gd\n", "xa/y/b.gd", false, false},
		{"double star after an escape stays in its segment", "\\a**/b.gd\n", "a/x/b.gd", false, false},
		{"trailing double star after a literal prefix", "/x/a**\n", "x/ab/c.gd", false, true},

		{"range across the separator does not match it", "a[+-0]b\n", "a/b", false, false},
		{"range across the separator keeps its lower part", "x/a[+-0]b\n", "x/a.b", false, true},
		{"range across the separator keeps its upper part", "x/a[+-0]b\n", "x/a0b", false, true},
		{"range ending at the separator", "x/a[+-/]b\n", "x/a/b", false, false},
		{"range starting at the separator", "x/a[/-1]b\n", "x/a1b", false, true},
		{"named class does not match the separator", "x/a[[:punct:]]b\n", "x/a/b", false, false},
		{"named class keeps its other members", "x/a[[:punct:]]b\n", "x/a.b", false, true},
		{"graph class does not match the separator", "x/a[[:graph:]]b\n", "x/a/b", false, false},
		{"print class matches a space", "x/a[[:print:]]b\n", "x/a b", false, true},
		{"hyphen last in a class is literal", "a[a-]b\n", "a-b", false, true},
		{"escaped hyphen in a class is literal", "a[a\\-z]b\n", "amb", false, false},
		{"range from a closing bracket", "a[]-a]b\n", "a_b", false, true},
		{"hyphen after a named class is literal", "a[[:alpha:]-z]b\n", "a-b", false, true},

		{"backslash escapes a star", "a\\*.gd\n", "a*.gd", false, true},
		{"escaped star is not a wildcard", "a\\*.gd\n", "ab.gd", false, false},
		{"backslash escapes a bracket", "\\[a].gd\n", "[a].gd", false, true},
		{"escaped exclamation mark is literal", "\\!keep.gd\n", "!keep.gd", false, true},

		{"last match wins when ignoring", "!a.gd\na.gd\n", "a.gd", false, true},
		{"last match wins when re-including", "*.gd\n!keep.gd\n", "keep.gd", false, false},
		{"re-include leaves the others ignored", "*.gd\n!keep.gd\n", "drop.gd", false, true},
		{"re-include applies at any depth", "*.gd\n!keep.gd\n", "x/keep.gd", false, false},
		{"negation alone ignores nothing", "!a.gd\n", "a.gd", false, false},

		{"ancestor match ignores the file", "/vendor/\n", "vendor/x/y/a.gd", false, true},
		{"re-include inside an ignored directory", "addons/\n!addons/our_plugin/\n", "addons/our_plugin/a.gd", false, false},
		{"re-include of the directory itself", "addons/\n!addons/our_plugin/\n", "addons/our_plugin", true, false},
		{"siblings of the re-include stay ignored", "addons/\n!addons/our_plugin/\n", "addons/other/a.gd", false, true},
		{"anchored re-include does not reach nested addons", "addons/\n!addons/our_plugin/\n", "apps/editor/addons/our_plugin/a.gd", false, true},
		{"ignore after a re-include wins again", "addons/\n!addons/our_plugin/\n*.pb.gd\n", "addons/our_plugin/a.pb.gd", false, true},
		{"re-included file inside an ignored directory", "generated/\n!generated/keep.gd\n", "generated/keep.gd", false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matcher := mustParse(t, test.source)
			if got := matcher.Ignored(test.path, test.isDirectory); got != test.want {
				t.Errorf("Ignored(%q, %v) with %q = %v, want %v", test.path, test.isDirectory, test.source, got, test.want)
			}
		})
	}
}

func TestNilMatcherIgnoresNothing(t *testing.T) {
	var matcher *Matcher
	if matcher.Ignored("addons/a.gd", false) {
		t.Error("a nil Matcher must ignore nothing")
	}
	if matcher.HasNegation() {
		t.Error("a nil Matcher has no negation")
	}
}

func TestHasNegation(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"", false},
		{"addons/\n*.pb.gd\n", false},
		{"addons/\n!addons/our_plugin/\n", true},
		{"\\!literal.gd\n", false},
		{"# !comment\n", false},
	}
	for _, test := range tests {
		if got := mustParse(t, test.source).HasNegation(); got != test.want {
			t.Errorf("HasNegation() with %q = %v, want %v", test.source, got, test.want)
		}
	}
}

func TestParseErrorsNameTheLine(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"unterminated character class", "a.gd\n\n[abc\n", "line 3: "},
		{"unterminated negated class", "[!\n", "line 1: "},
		{"lone exclamation mark", "# header\n!\n", "line 2: "},
		{"trailing lone backslash", "a.gd\r\nb\\\r\n", "line 2: "},
		{"reversed range", "ok\nok\nok\n[z-a].gd\n", `line 4: character class range "z-a" runs backwards`},
		{"class of the separator alone", "[/]\n", `line 1: character class "[/]" matches nothing, because a class never matches "/"`},
		{"class of a range of separators", "a[/-/]\n", `line 1: character class "[/-/]" matches nothing, because a class never matches "/"`},
		{"unknown class name", "a[[:bogus:]]\n", `line 1: unknown character class name "[:bogus:]"`},
		{"unterminated class name", "a[[:alpha\n", "line 1: unterminated character class"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matcher, err := Parse([]byte(test.source))
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", test.source)
			}
			if matcher != nil {
				t.Errorf("Parse(%q) returned a matcher alongside its error", test.source)
			}
			if !strings.HasPrefix(err.Error(), test.want) {
				t.Errorf("error = %q, want prefix %q", err, test.want)
			}
		})
	}
}

// The base semantics are checked against git itself, so the matcher cannot
// drift from the syntax its users already know. Negation is left out because
// re-including inside an ignored directory deliberately differs from git.
func TestIgnoredAgreesWithGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	paths := []string{
		"a.gd",
		"ab.gd",
		"b1.gd",
		"keep.gd",
		"my file.gd",
		"addons/plugin/a.gd",
		"addons/plugin/deep/b1.gd",
		"apps/editor/addons/tool.gd",
		"apps/editor/main.gd",
		"apps/addons",
		"client/protocol/messages.pb.gd",
		"client/protocol/sub/more.pb.gd",
		"client/protocol/plain.gd",
		"apps/client/protocol/plain.gd",
		"generated/x/y/a.gd",
		"generated/top.gd",
		"x/generated/a.gd",
		"a/b.gd",
		"a/x/b.gd",
		"a/x/y/b.gd",
		"x/a/b.gd",
		"x/aXXb.gd",
		"x/[a].gd",
		"x/#hash.gd",
		"x/!bang.gd",
		"a/b",
		"a/x/b",
		"a/x/y/b",
		"a.b",
		"a-b",
		"a+b",
		"a,b",
		"a0b",
		"a]b",
		"a_b",
		"aab",
		"ab",
		"abb",
		"ax/b",
		"axx/q/b",
		"x/y/a",
		"x/yy/z/w",
		"x/ab",
	}
	sources := []string{
		"addons/\n",
		"addons\n",
		"/addons/\n",
		"*.pb.gd\n",
		"/client/protocol/\n",
		"client/protocol/*.gd\n",
		"client/*.gd\n",
		"protocol/\n",
		"/a.gd\n",
		"a?.gd\n",
		"[ab]?.gd\n",
		"[a-c]1.gd\n",
		"[!a]*.gd\n",
		"**/generated\n",
		"**/generated/a.gd\n",
		"generated/**\n",
		"a/**/b.gd\n",
		"a/**\n",
		"/**/b.gd\n",
		"a**b.gd\n",
		"\\[a].gd\n",
		"\\#hash.gd\n",
		"\\!bang.gd\n",
		"# comment\nkeep.gd   \n\nmy file.gd\n",
		"keep.gd # not a comment\n",
		"addons/\r\n*.pb.gd\r\n",
		"plugin/deep\n",
		"apps/*/addons/\n",
		"x/*\n",
		"*\n",
		"**\n",
		"/**\n",
		"deep/\nmain.gd\n/generated/top.gd\n",
		"a[+-0]b\n",
		"a[,-0]b\n",
		"a[+-/]b\n",
		"a[.-0]b\n",
		"a[--0]b\n",
		"a[\\--0]b\n",
		"a[[:punct:]]b\n",
		"a[[:graph:]]b\n",
		"a[[:print:]]b\n",
		"a[[:alpha:]-z]b\n",
		"a[a-]b\n",
		"a[]]b\n",
		"a[]-a]b\n",
		"a[!]]b\n",
		"a[\\]]b\n",
		"a[a\\-z]b\n",
		"a[^/]b\n",
		"a[!/]b\n",
		"x/[!a]b\n",
		"a**/b\n",
		"***/a\n",
		"/a**\n",
		"x/y**\n",
		"x/y**/w\n",
		"*a**/b\n",
		"\\a**/b\n",
		"a?**/b\n",
		"/**b\n",
		"/a**b\n",
		"a/***/b\n",
		"x/***/w\n",
		"***\n",
		"/***\n",
		"a/***\n",
	}
	environment := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
	)
	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			initialize := exec.Command(git, "init", "--quiet", root)
			initialize.Env = environment
			if output, err := initialize.CombinedOutput(); err != nil {
				t.Skipf("git init failed: %v: %s", err, output)
			}
			for _, path := range paths {
				absolute := filepath.Join(root, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(absolute, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			check := exec.Command(git, "-C", root, "check-ignore", "-z", "--stdin")
			check.Env = environment
			check.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
			output, err := check.Output()
			// check-ignore exits 1 when no path is ignored.
			if exitError, ok := err.(*exec.ExitError); err != nil && (!ok || exitError.ExitCode() != 1) {
				t.Fatalf("git check-ignore: %v", err)
			}
			var want []string
			for _, path := range bytes.Split(output, []byte{0}) {
				if len(path) > 0 {
					want = append(want, string(path))
				}
			}
			matcher := mustParse(t, source)
			var got []string
			for _, path := range paths {
				if matcher.Ignored(path, false) {
					got = append(got, path)
				}
			}
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("ignored paths differ from git\n got: %q\nwant: %q", got, want)
			}
		})
	}
}
