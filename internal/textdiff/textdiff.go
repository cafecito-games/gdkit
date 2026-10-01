// Package textdiff renders the difference between two texts as a unified diff.
package textdiff

import (
	"bytes"
	"fmt"
	"strings"
)

// contextLines is how many unchanged lines surround each change in a hunk.
const contextLines = 3

type operationKind byte

const (
	keep   operationKind = ' '
	remove operationKind = '-'
	insert operationKind = '+'
)

// operation is one line of an edit script.
type operation struct {
	kind operationKind
	text string
}

// match pairs a line of the old text with the equal line of the new text.
type match struct {
	oldIndex, newIndex int
}

// Unified returns the line-based unified diff that turns old into new, with
// three lines of context around each change. It returns the empty string when
// the two are identical.
func Unified(oldName, newName string, old, new []byte) string {
	if bytes.Equal(old, new) {
		return ""
	}
	operations := editScript(splitLines(old), splitLines(new))
	var output strings.Builder
	fmt.Fprintf(&output, "--- %s\n+++ %s\n", oldName, newName)
	writeHunks(&output, operations)
	return output.String()
}

// splitLines cuts text into lines that keep their terminator, so a final line
// without a newline compares unequal to the same line with one.
func splitLines(text []byte) []string {
	if len(text) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(text), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// editScript returns the kept, removed, and inserted lines that turn old into
// new, in output order.
func editScript(old, new []string) []operation {
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix && old[len(old)-1-suffix] == new[len(new)-1-suffix] {
		suffix++
	}
	oldEnd, newEnd := len(old)-suffix, len(new)-suffix

	operations := make([]operation, 0, len(old)+len(new))
	for _, line := range old[:prefix] {
		operations = append(operations, operation{keep, line})
	}
	oldIndex, newIndex := prefix, prefix
	flush := func(oldStop, newStop int) {
		for ; oldIndex < oldStop; oldIndex++ {
			operations = append(operations, operation{remove, old[oldIndex]})
		}
		for ; newIndex < newStop; newIndex++ {
			operations = append(operations, operation{insert, new[newIndex]})
		}
	}
	for _, pair := range matchLines(old[prefix:oldEnd], new[prefix:newEnd]) {
		flush(prefix+pair.oldIndex, prefix+pair.newIndex)
		operations = append(operations, operation{keep, old[oldIndex]})
		oldIndex++
		newIndex++
	}
	flush(oldEnd, newEnd)
	for _, line := range old[oldEnd:] {
		operations = append(operations, operation{keep, line})
	}
	return operations
}

// matchLines returns a longest common subsequence of old and new as index
// pairs in increasing order.
//
// A line that appears on only one side can never be matched, so those lines
// are set aside before the search. The search costs time and memory
// proportional to the square of the number of differences it has to step over,
// and a reformatted file is mostly lines the other side does not have; without
// this a file whose every line was reindented would be the worst case.
func matchLines(old, new []string) []match {
	inOld := make(map[string]bool, len(old))
	for _, line := range old {
		inOld[line] = true
	}
	inNew := make(map[string]bool, len(new))
	for _, line := range new {
		inNew[line] = true
	}
	oldLines, oldIndexes := shared(old, inNew)
	newLines, newIndexes := shared(new, inOld)
	matches := shortestEdit(oldLines, newLines)
	for index, pair := range matches {
		matches[index] = match{oldIndexes[pair.oldIndex], newIndexes[pair.newIndex]}
	}
	return matches
}

// shared returns the lines present in other, with the index each had in lines.
func shared(lines []string, other map[string]bool) ([]string, []int) {
	kept := make([]string, 0, len(lines))
	indexes := make([]int, 0, len(lines))
	for index, line := range lines {
		if other[line] {
			kept = append(kept, line)
			indexes = append(indexes, index)
		}
	}
	return kept, indexes
}

// shortestEdit is Myers' greedy algorithm: it advances the furthest-reaching
// path on every diagonal one edit at a time, then walks the recorded frontiers
// backwards to recover the matched lines.
func shortestEdit(old, new []string) []match {
	if len(old) == 0 || len(new) == 0 {
		return nil
	}
	limit := len(old) + len(new)
	// frontier[limit+k] is the furthest old index reached on diagonal k, where
	// k is the old index minus the new index.
	frontier := make([]int32, 2*limit+2)
	var history [][]int32
	distance := 0
search:
	for ; distance <= limit; distance++ {
		// Only diagonals -distance..distance can change in this round, so that
		// window is all the backward walk needs from the previous round.
		history = append(history, append([]int32(nil), frontier[limit-distance:limit+distance+1]...))
		for diagonal := -distance; diagonal <= distance; diagonal += 2 {
			var oldIndex int
			if diagonal == -distance || (diagonal != distance && frontier[limit+diagonal-1] < frontier[limit+diagonal+1]) {
				oldIndex = int(frontier[limit+diagonal+1])
			} else {
				oldIndex = int(frontier[limit+diagonal-1]) + 1
			}
			newIndex := oldIndex - diagonal
			for oldIndex < len(old) && newIndex < len(new) && old[oldIndex] == new[newIndex] {
				oldIndex++
				newIndex++
			}
			frontier[limit+diagonal] = int32(oldIndex)
			if oldIndex >= len(old) && newIndex >= len(new) {
				break search
			}
		}
	}

	var matches []match
	oldIndex, newIndex := len(old), len(new)
	for ; distance > 0; distance-- {
		// previous holds diagonals -distance..distance as they stood before
		// this round, so diagonal k sits at offset distance+k.
		previous := history[distance]
		diagonal := oldIndex - newIndex
		var previousDiagonal int
		if diagonal == -distance || (diagonal != distance && previous[distance+diagonal-1] < previous[distance+diagonal+1]) {
			previousDiagonal = diagonal + 1
		} else {
			previousDiagonal = diagonal - 1
		}
		previousOld := int(previous[distance+previousDiagonal])
		previousNew := previousOld - previousDiagonal
		for oldIndex > previousOld && newIndex > previousNew {
			oldIndex--
			newIndex--
			matches = append(matches, match{oldIndex, newIndex})
		}
		oldIndex, newIndex = previousOld, previousNew
	}
	for oldIndex > 0 && newIndex > 0 {
		oldIndex--
		newIndex--
		matches = append(matches, match{oldIndex, newIndex})
	}
	for left, right := 0, len(matches)-1; left < right; left, right = left+1, right-1 {
		matches[left], matches[right] = matches[right], matches[left]
	}
	return matches
}

// writeHunks groups the changes of an edit script into hunks. Changes closer
// together than twice the context share a hunk, since their context would
// otherwise overlap.
func writeHunks(output *strings.Builder, operations []operation) {
	// oldLine and newLine count the lines of each side that precede the
	// operation at index.
	oldLine, newLine := 0, 0
	index := 0
	for index < len(operations) {
		if operations[index].kind == keep {
			oldLine++
			newLine++
			index++
			continue
		}
		start := max(index-contextLines, 0)
		end, lastChange := index, index
		for end < len(operations) && end-lastChange <= 2*contextLines {
			if operations[end].kind != keep {
				lastChange = end
			}
			end++
		}
		end = min(lastChange+contextLines+1, len(operations))

		oldStart, newStart := oldLine-(index-start), newLine-(index-start)
		oldCount, newCount := 0, 0
		for _, current := range operations[start:end] {
			if current.kind != insert {
				oldCount++
			}
			if current.kind != remove {
				newCount++
			}
		}
		fmt.Fprintf(output, "@@ -%d,%d +%d,%d @@\n", hunkStart(oldStart, oldCount), oldCount, hunkStart(newStart, newCount), newCount)
		for _, current := range operations[start:end] {
			output.WriteByte(byte(current.kind))
			output.WriteString(current.text)
			if !strings.HasSuffix(current.text, "\n") {
				output.WriteString("\n\\ No newline at end of file\n")
			}
		}
		oldLine, newLine = oldStart+oldCount, newStart+newCount
		index = end
	}
}

// hunkStart is the 1-based line a hunk range starts on. An empty range names
// the line before it instead, which is how the format marks a pure insertion
// or deletion.
func hunkStart(preceding, count int) int {
	if count == 0 {
		return preceding
	}
	return preceding + 1
}
