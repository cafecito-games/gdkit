package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegionExtentOwnsTheLeadingGapOnly(t *testing.T) {
	source := []byte("var q: int\n\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n\n\nvar r: int\n")
	span, found, err := FindRegion(source)
	require.NoError(t, err)
	require.True(t, found, "FindRegion found no region")
	want := "\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	got := string(source[span.Start:span.End])
	assert.Equal(t, want, got)
	assert.Equal(t, []byte("\n\nvar r: int\n"), source[span.End:], "the trailing gap was taken into the span")
}

func TestRegionExtentAtStartOfFile(t *testing.T) {
	source := []byte("# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n")
	span, found, err := FindRegion(source)
	require.NoError(t, err)
	require.True(t, found, "FindRegion found no region")
	assert.Equal(t, 0, span.Start)
	assert.Equal(t, len(source), span.End)
}

func TestRegionExtentWithNoTrailingNewline(t *testing.T) {
	source := []byte("var q: int\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end")
	span, found, err := FindRegion(source)
	require.NoError(t, err)
	require.True(t, found, "FindRegion found no region")
	assert.Equal(t, len(source), span.End)
}

func TestRegionRejectsASecondRegionAndAnUnterminatedOne(t *testing.T) {
	two := []byte("# gdkit:generated:begin\n# gdkit:generated:end\n# gdkit:generated:begin\n# gdkit:generated:end\n")
	_, _, err := FindRegion(two)
	assert.Error(t, err, "two regions were accepted")
	open := []byte("# gdkit:generated:begin\nfunc f():\n\tpass\n")
	_, _, err = FindRegion(open)
	assert.Error(t, err, "an unterminated region was accepted")
	orphanEnd := []byte("func f():\n\tpass\n# gdkit:generated:end\n")
	_, _, err = FindRegion(orphanEnd)
	assert.Error(t, err, "an end sentinel with no begin was accepted")
}

func TestRegionNotFound(t *testing.T) {
	_, found, err := FindRegion([]byte("var q: int\n"))
	assert.False(t, found, "FindRegion found a region where none was expected")
	assert.NoError(t, err)
}

// A sentinel inside a string literal is not a sentinel: matching is on whole
// lines, not substrings.
func TestRegionIgnoresASentinelInsideAStringLiteral(t *testing.T) {
	source := []byte("var s = \"# gdkit:generated:begin\"\n")
	_, found, err := FindRegion(source)
	assert.False(t, found, "FindRegion found a region where none was expected")
	assert.NoError(t, err)
}

func TestSpliceAppendsWithTheOwnedGap(t *testing.T) {
	source := []byte("class_name Hex\n\nvar q: int\n")
	region := "# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	got, span, err := Splice(source, region, 2)
	require.NoError(t, err)
	want := "class_name Hex\n\nvar q: int\n\n\n" + region
	assert.Equal(t, want, string(got))
	assert.Equal(t, "\n\n"+region, string(got[span.Start:span.End]), "the span omitted the gap and the region")
}

func TestSpliceReplacesInPlaceAndKeepsEverythingElse(t *testing.T) {
	source := []byte("var q: int\n\n\n# gdkit:generated:begin\nold\n# gdkit:generated:end\n\nvar r: int\n")
	region := "# gdkit:generated:begin\nnew\n# gdkit:generated:end\n"
	got, _, err := Splice(source, region, 2)
	require.NoError(t, err)
	want := "var q: int\n\n\n" + region + "\nvar r: int\n"
	assert.Equal(t, want, string(got))
}

func TestSpliceAtStartOfFileEmitsNoLeadingGap(t *testing.T) {
	region := "# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	got, span, err := Splice(nil, region, 2)
	require.NoError(t, err)
	assert.Equal(t, region, string(got))
	assert.Equal(t, 0, span.Start)
}

// Splicing into a file whose last line has no terminator must still produce a
// parseable result, and splicing again must not drift.
func TestSpliceIsIdempotent(t *testing.T) {
	region := "# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	for _, source := range []string{
		"class_name Hex\n\nvar q: int\n",
		"class_name Hex\n\nvar q: int",
		"",
	} {
		once, _, err := Splice([]byte(source), region, 2)
		require.NoError(t, err, source)
		twice, _, err := Splice(once, region, 2)
		require.NoError(t, err, source)
		assert.Equal(t, once, twice, source+": a second splice changed it")
	}
}
