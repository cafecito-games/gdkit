package generate

import (
	"bytes"
	"fmt"

	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser"
	gdformat "github.com/cafecito-games/gdparser/format"
)

// verify checks a candidate before it is offered.
//
// format's safety net cannot be reused: its value is the promise that a rewrite
// changes nothing but layout, enforced by comparing the token stream, and a
// generator changes the token stream by definition. Extending format.unsafe to
// carve out an exception would weaken the one invariant that makes format write
// trustworthy.
//
// The replacement is tighter and cheaper: every byte outside the region is
// identical, the result reparses, and a file that was canonical stays canonical.
func (g *Generator) verify(script *project.Script, class *Class, candidate Candidate) error {
	before, after := len(script.Source), []byte(nil)
	if class.HasRegion {
		before = class.Region.Start
		after = script.Source[class.Region.End:]
	}
	if !bytes.Equal(script.Source[:before], candidate.Contents[:candidate.Region.Start]) {
		return fmt.Errorf("bytes before the generated region changed")
	}
	if !bytes.Equal(after, candidate.Contents[candidate.Region.End:]) {
		return fmt.Errorf("bytes after the generated region changed")
	}
	if _, err := gdparser.ParseFile(candidate.Path, candidate.Contents); err != nil {
		return fmt.Errorf("the result does not parse: %w", err)
	}
	return g.verifyFormatOracle(script, candidate)
}

// verifyFormatOracle runs the project's full-file formatter over the candidate
// as a read-only check, when the source file was already canonical.
//
// Owning the region's leading gap is an argument that a canonical file stays
// canonical, not a proof, and the argument is about another package's
// behaviour: gdparser's blankLineGaps attributes the gap before a comment run
// documenting a declaration to the top of that run. So the claim is checked on
// every real candidate rather than only in fixtures, which makes it a
// precondition of writing instead of a claim in a document.
func (g *Generator) verifyFormatOracle(script *project.Script, candidate Candidate) error {
	if !g.canonical(script.Path, script.Source) {
		// The file was not canonical to begin with, so gen owes it nothing
		// beyond leaving the rest of it alone, which verify already checked.
		return nil
	}
	if !g.canonical(candidate.Path, candidate.Contents) {
		return fmt.Errorf("the result is not formatted, although the source was")
	}
	return nil
}

func (g *Generator) canonical(path string, contents []byte) bool {
	file, err := gdparser.ParseFile(path, contents)
	if err != nil {
		return false
	}
	return gdformat.FileWithOptions(file, g.options) == string(contents)
}
