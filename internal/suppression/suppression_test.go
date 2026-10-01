package suppression

import (
	"reflect"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := map[string]struct {
		kind   Kind
		line   string
		offset int
		names  []string
	}{
		"ignore":           {Ignore, "# gdlint:ignore = a, b", 0, []string{"a", "b"}},
		"trailing ignore":  {Ignore, "var x = 1  #gdlint:ignore=a", 11, []string{"a"}},
		"disable":          {Disable, "# gdlint: disable = a", 0, []string{"a"}},
		"enable":           {Enable, "\t# gdkit : enable=a ,b remark", 1, []string{"a", "b remark"}},
		"gdkit spelling":   {Ignore, "# gdkit:ignore = a", 0, []string{"a"}},
		"inside a string":  {Ignore, `var s = "# gdlint:ignore = a"`, 9, []string{`a"`}},
		"other directive":  {Disable, "# gdlint:ignore = a", 0, nil},
		"ordinary comment": {Ignore, "# ignore = a", 0, nil},
		"no rule list":     {Ignore, "# gdlint:ignore", 0, nil},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			offset, names, ok := Match(testCase.kind, testCase.line)
			if ok != (testCase.names != nil) {
				t.Fatalf("ok = %v", ok)
			}
			if !ok {
				return
			}
			if offset != testCase.offset || !reflect.DeepEqual(names, testCase.names) {
				t.Fatalf("got offset %d names %q, want %d %q", offset, names, testCase.offset, testCase.names)
			}
		})
	}
}

func TestStandsAlone(t *testing.T) {
	for line, want := range map[string]bool{
		"# gdlint:ignore = a":            true,
		"\t\t# gdlint:ignore = a":        true,
		"var x = 1  # gdlint:ignore = a": false,
		"func f():  # gdlint:ignore = a": false,
	} {
		if got := StandsAlone(line); got != want {
			t.Errorf("StandsAlone(%q) = %v, want %v", line, got, want)
		}
	}
}
