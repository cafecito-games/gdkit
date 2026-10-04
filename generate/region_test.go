package generate

import (
	"bytes"
	"testing"
)

func TestRegionExtentOwnsTheLeadingGapOnly(t *testing.T) {
	source := []byte("var q: int\n\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n\n\nvar r: int\n")
	span, found, err := FindRegion(source)
	if err != nil || !found {
		t.Fatalf("FindRegion = %v, %v", found, err)
	}
	want := "\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	if got := string(source[span.Start:span.End]); got != want {
		t.Errorf("span = %q, want %q", got, want)
	}
	if !bytes.Equal(source[span.End:], []byte("\n\nvar r: int\n")) {
		t.Errorf("the trailing gap was taken into the span: %q", source[span.End:])
	}
}

func TestRegionExtentAtStartOfFile(t *testing.T) {
	source := []byte("# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n")
	span, found, err := FindRegion(source)
	if err != nil || !found {
		t.Fatalf("FindRegion = %v, %v", found, err)
	}
	if span.Start != 0 || span.End != len(source) {
		t.Errorf("span = %+v, want the whole file", span)
	}
}

func TestRegionExtentWithNoTrailingNewline(t *testing.T) {
	source := []byte("var q: int\n\n# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end")
	span, found, err := FindRegion(source)
	if err != nil || !found {
		t.Fatalf("FindRegion = %v, %v", found, err)
	}
	if span.End != len(source) {
		t.Errorf("End = %d, want %d", span.End, len(source))
	}
}

func TestRegionRejectsASecondRegionAndAnUnterminatedOne(t *testing.T) {
	two := []byte("# gdkit:generated:begin\n# gdkit:generated:end\n# gdkit:generated:begin\n# gdkit:generated:end\n")
	if _, _, err := FindRegion(two); err == nil {
		t.Error("two regions were accepted")
	}
	open := []byte("# gdkit:generated:begin\nfunc f():\n\tpass\n")
	if _, _, err := FindRegion(open); err == nil {
		t.Error("an unterminated region was accepted")
	}
	orphanEnd := []byte("func f():\n\tpass\n# gdkit:generated:end\n")
	if _, _, err := FindRegion(orphanEnd); err == nil {
		t.Error("an end sentinel with no begin was accepted")
	}
}

func TestRegionNotFound(t *testing.T) {
	span, found, err := FindRegion([]byte("var q: int\n"))
	if found || err != nil {
		t.Errorf("FindRegion = %+v, %v, %v, want not found", span, found, err)
	}
}

// A sentinel inside a string literal is not a sentinel: matching is on whole
// lines, not substrings.
func TestRegionIgnoresASentinelInsideAStringLiteral(t *testing.T) {
	source := []byte("var s = \"# gdkit:generated:begin\"\n")
	if _, found, err := FindRegion(source); found || err != nil {
		t.Errorf("FindRegion = %v, %v, want not found", found, err)
	}
}

func TestSpliceAppendsWithTheOwnedGap(t *testing.T) {
	source := []byte("class_name Hex\n\nvar q: int\n")
	region := "# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	got, span, err := Splice(source, region, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "class_name Hex\n\nvar q: int\n\n\n" + region
	if string(got) != want {
		t.Errorf("Splice = %q, want %q", got, want)
	}
	if string(got[span.Start:span.End]) != "\n\n"+region {
		t.Errorf("span = %q, want the gap and the region", got[span.Start:span.End])
	}
}

func TestSpliceReplacesInPlaceAndKeepsEverythingElse(t *testing.T) {
	source := []byte("var q: int\n\n\n# gdkit:generated:begin\nold\n# gdkit:generated:end\n\nvar r: int\n")
	region := "# gdkit:generated:begin\nnew\n# gdkit:generated:end\n"
	got, _, err := Splice(source, region, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "var q: int\n\n\n" + region + "\nvar r: int\n"
	if string(got) != want {
		t.Errorf("Splice = %q, want %q", got, want)
	}
}

func TestSpliceAtStartOfFileEmitsNoLeadingGap(t *testing.T) {
	region := "# gdkit:generated:begin\nfunc f():\n\tpass\n# gdkit:generated:end\n"
	got, span, err := Splice(nil, region, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != region {
		t.Errorf("Splice = %q, want %q", got, region)
	}
	if span.Start != 0 {
		t.Errorf("Start = %d, want 0", span.Start)
	}
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
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		twice, _, err := Splice(once, region, 2)
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		if !bytes.Equal(once, twice) {
			t.Errorf("%q: a second splice changed it:\n%q\n%q", source, once, twice)
		}
	}
}
