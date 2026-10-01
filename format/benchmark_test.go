package format

import (
	"fmt"
	"strings"
	"testing"
)

// syntheticProject builds count unformatted scripts, each large enough that
// verification, not discovery, dominates a run.
func syntheticProject(count int) map[string]string {
	var script strings.Builder
	script.WriteString("extends Node\n#header\nconst LIMIT=0XFF\nvar names=['a','b',\"c\"]\n")
	for index := range 40 {
		fmt.Fprintf(&script, "func method_%d( value,other=.5 ):\n", index)
		script.WriteString("\t#explain\n\tif value>other&&!is_ready( ):\n\t\treturn value*2+other  #why\n")
		script.WriteString("\tfor item in names:\n\t\tprint( 'item %s' % item,{'key':value,\"other\":[1,2,3]} )\n")
		script.WriteString("\tmatch value:\n\t\t1:\n\t\t\treturn 'one'\n\t\t_:\n\t\t\tpass\n\treturn other\n")
	}
	files := make(map[string]string, count)
	for index := range count {
		files[fmt.Sprintf("scripts/group_%02d/script_%03d.gd", index%10, index)] = script.String()
	}
	return files
}

func BenchmarkFormat(b *testing.B) {
	config := DefaultConfig()
	snapshot := loadProjectInto(b, b.TempDir(), config, syntheticProject(100))
	formatter, err := New(config)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		report := formatter.Format(snapshot)
		if report.HasDiagnostics() || len(report.Changed()) != len(snapshot.Paths) {
			b.Fatalf("unexpected report: %d changed, %d diagnostics", len(report.Changed()), len(report.Diagnostics))
		}
	}
}
