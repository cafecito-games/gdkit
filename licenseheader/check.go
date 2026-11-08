package licenseheader

import (
	"bytes"
	"strings"

	"github.com/cafecito-games/gdkit/project"
)

const (
	beginMarker = "gdkit:license:begin"
	endMarker   = "gdkit:license:end"
)

// Manager plans the managed block selected by one validated configuration.
type Manager struct {
	header []string
}

// New validates config and returns a reusable license-header manager.
func New(config Config) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Manager{header: append([]string(nil), config.Header...)}, nil
}

// Check plans license header insertions and updates without writing anything.
// An empty header is an intentional no-op, including for files that happen to
// contain marker-like comments.
func (m *Manager) Check(snapshot *project.Snapshot) Report {
	report := Report{Results: []Result{}, Diagnostics: []Diagnostic{}}
	for _, path := range snapshot.Selected {
		script := snapshot.Scripts[path]
		if script == nil {
			continue
		}
		if len(m.header) == 0 {
			report.Results = append(report.Results, Result{Path: path})
			continue
		}
		contents, changed, diagnostic := plan(path, script.Source, m.header)
		if diagnostic != nil {
			report.Diagnostics = append(report.Diagnostics, *diagnostic)
			continue
		}
		result := Result{Path: path, Changed: changed}
		if changed {
			result.Contents = contents
		}
		report.Results = append(report.Results, result)
	}
	report.sort()
	return report
}

type sourceLine struct {
	start int
	end   int
	text  string
	line  int
}

func plan(path string, source []byte, header []string) ([]byte, bool, *Diagnostic) {
	lines := sourceLines(source)
	begins, ends := markerLines(lines)
	insertion := insertionOffset(source)
	if len(begins) != len(ends) || len(begins) > 1 ||
		(len(begins) == 1 && (begins[0].start > ends[0].start || begins[0].start != insertion)) {
		line := 1
		if len(begins) > 0 {
			line = begins[0].line
		} else if len(ends) > 0 {
			line = ends[0].line
		}
		return nil, false, &Diagnostic{
			Rule:    ruleMarker,
			Message: "license header has malformed or misplaced gdkit managed-block markers",
			Path:    path,
			Line:    line,
			Column:  1,
		}
	}

	newline := newlineFor(source)
	block := render(header, newline)
	start, end := insertion, insertion
	if len(begins) == 1 {
		start, end = begins[0].start, ends[0].end
		// The blank separator emitted by gdkit belongs to the block. Consume
		// one when updating so changing the header never accumulates blanks.
		for _, line := range lines {
			if line.start == end && line.text == "" {
				end = line.end
				break
			}
		}
	}

	contents := make([]byte, 0, len(source)-(end-start)+len(block))
	contents = append(contents, source[:start]...)
	startsAfterBOM := start == 3 && bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf})
	if start > 0 && !startsAfterBOM && source[start-1] != '\n' && source[start-1] != '\r' {
		contents = append(contents, newline...)
	}
	contents = append(contents, block...)
	contents = append(contents, source[end:]...)
	if bytes.Equal(source, contents) {
		return nil, false, nil
	}
	return contents, true, nil
}

func sourceLines(source []byte) []sourceLine {
	lines := []sourceLine{}
	start, number := 0, 1
	for start < len(source) {
		relative := bytes.IndexByte(source[start:], '\n')
		end := len(source)
		if relative >= 0 {
			end = start + relative + 1
		}
		textEnd := end
		if textEnd > start && source[textEnd-1] == '\n' {
			textEnd--
		}
		if textEnd > start && source[textEnd-1] == '\r' {
			textEnd--
		}
		lineStart := start
		if number == 1 && bytes.HasPrefix(source[start:textEnd], []byte{0xef, 0xbb, 0xbf}) {
			lineStart += 3
		}
		lines = append(lines, sourceLine{start: lineStart, end: end, text: string(source[lineStart:textEnd]), line: number})
		start, number = end, number+1
	}
	return lines
}

func markerLines(lines []sourceLine) (begins, ends []sourceLine) {
	for _, line := range lines {
		if !strings.HasPrefix(line.text, "#") {
			continue
		}
		switch strings.TrimSpace(strings.TrimPrefix(line.text, "#")) {
		case beginMarker:
			begins = append(begins, line)
		case endMarker:
			ends = append(ends, line)
		}
	}
	return begins, ends
}

func newlineFor(source []byte) []byte {
	if index := bytes.IndexByte(source, '\n'); index > 0 && source[index-1] == '\r' {
		return []byte("\r\n")
	}
	return []byte("\n")
}

func render(header []string, newline []byte) []byte {
	var block bytes.Buffer
	writeCommentLine(&block, beginMarker, newline)
	for _, line := range header {
		writeCommentLine(&block, line, newline)
	}
	writeCommentLine(&block, endMarker, newline)
	block.Write(newline)
	return block.Bytes()
}

func writeCommentLine(block *bytes.Buffer, text string, newline []byte) {
	block.WriteByte('#')
	if text != "" {
		block.WriteByte(' ')
	}
	block.WriteString(text)
	block.Write(newline)
}

func insertionOffset(source []byte) int {
	start := 0
	if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) {
		start = 3
	}
	if bytes.HasPrefix(source[start:], []byte("#!")) {
		if end := bytes.IndexByte(source[start:], '\n'); end >= 0 {
			return start + end + 1
		}
		return len(source)
	}
	return start
}
