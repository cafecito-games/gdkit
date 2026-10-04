package generate

import (
	"bytes"
	"fmt"
	"strings"
)

// Span is a byte range in a file: [Start, End).
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Empty reports a zero span, which is what a file with no region carries.
func (s Span) Empty() bool { return s.Start == 0 && s.End == 0 }

// FindRegion locates the generated region in source.
//
// The span runs from the first byte of the begin-sentinel line to the newline
// ending the end-sentinel line, extended backwards over every immediately
// preceding blank line. Trailing blank lines after the end sentinel are not
// owned: they belong to the gap before whatever follows.
//
// The asymmetry is not arbitrary. gdparser's blankLineGaps attributes the gap
// before a comment run that documents a declaration to the top of that run,
// and the begin sentinel is such a run sitting above a func. So the formatter
// treats the leading gap as part of the region and the trailing gap as part of
// the next statement, and owning exactly the leading one is what lets the
// region be formatted in isolation without disturbing bytes outside it.
func FindRegion(source []byte) (Span, bool, error) {
	begin := indexOfSentinelLineFrom(source, beginSentinel, 0)
	if begin < 0 {
		if indexOfSentinelLineFrom(source, endSentinel, 0) >= 0 {
			return Span{}, false, fmt.Errorf("a %s has no %s", endSentinel, beginSentinel)
		}
		return Span{}, false, nil
	}
	end := indexOfSentinelLineFrom(source, endSentinel, begin+1)
	if end < 0 {
		return Span{}, false, fmt.Errorf("a %s has no %s", beginSentinel, endSentinel)
	}
	if indexOfSentinelLineFrom(source, beginSentinel, end+1) >= 0 {
		return Span{}, false, fmt.Errorf("a class holds more than one generated region")
	}
	stop := len(source)
	if newline := bytes.IndexByte(source[end:], '\n'); newline >= 0 {
		stop = end + newline + 1
	}
	return Span{Start: extendOverLeadingBlankLines(source, begin), End: stop}, true, nil
}

// extendOverLeadingBlankLines walks back from the start of the line at offset
// over lines holding nothing but whitespace.
func extendOverLeadingBlankLines(source []byte, offset int) int {
	for offset > 0 {
		previousEnd := offset - 1
		previousStart := bytes.LastIndexByte(source[:previousEnd], '\n') + 1
		if strings.TrimSpace(string(source[previousStart:previousEnd])) != "" {
			return offset
		}
		offset = previousStart
	}
	return offset
}

// indexOfSentinelLineFrom returns the offset at which the line holding
// sentinel as its only content besides whitespace begins, or -1. Matching whole
// lines rather than substrings is what keeps a sentinel inside a string
// literal from being mistaken for one.
func indexOfSentinelLineFrom(source []byte, sentinel string, from int) int {
	for offset := from; offset <= len(source); {
		lineEnd := len(source)
		if newline := bytes.IndexByte(source[offset:], '\n'); newline >= 0 {
			lineEnd = offset + newline
		}
		if strings.TrimSpace(string(source[offset:lineEnd])) == sentinel {
			return offset
		}
		if lineEnd == len(source) {
			return -1
		}
		offset = lineEnd + 1
	}
	return -1
}

// Splice puts region into source, replacing an existing region or appending
// one at end of file, and returns the new contents with the region's span
// within them.
//
// gap is the number of blank lines the region owns before itself, which is
// blank_lines.top_level except at the start of a file, where there is nothing
// to separate from. The span is recovered by running FindRegion over the
// result rather than computed here, so the two can never disagree about what
// the region's extent is.
func Splice(source []byte, region string, gap int) ([]byte, Span, error) {
	span, found, err := FindRegion(source)
	if err != nil {
		return nil, Span{}, err
	}
	prefix, suffix := source, []byte(nil)
	if found {
		prefix, suffix = source[:span.Start], source[span.End:]
	}
	leading := strings.Repeat("\n", gap)
	switch {
	case len(prefix) == 0:
		leading = ""
	case !bytes.HasSuffix(prefix, []byte("\n")):
		// A file whose last line has no terminator needs one before the
		// sentinel, on top of the gap.
		leading = "\n" + leading
	}
	var out bytes.Buffer
	out.Grow(len(prefix) + len(leading) + len(region) + len(suffix))
	out.Write(prefix)
	out.WriteString(leading)
	out.WriteString(region)
	out.Write(suffix)
	contents := out.Bytes()
	spliced, found, err := FindRegion(contents)
	if err != nil || !found {
		return nil, Span{}, fmt.Errorf("the spliced region could not be located again: %v", err)
	}
	return contents, spliced, nil
}
