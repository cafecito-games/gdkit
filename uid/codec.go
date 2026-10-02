// Package uid generates and validates the uid:// identifiers Godot keeps in
// .uid sidecar files beside GDScript sources. The encoding and the way ids are
// drawn match Godot's ResourceUID exactly, so a sidecar this package writes is
// indistinguishable from one the editor wrote.
package uid

import (
	"strings"
)

// Prefix precedes every uid identifier.
const Prefix = "uid://"

// alphabet holds the characters Godot renders an id with, in digit order, and
// so fixes the base at 34. It is missing z and 9 on purpose: Godot computes
// its digit count as 'z' - 'a', which is 25 rather than the 26 letters, and
// its base as that plus '9' - '0', which is 34 rather than 36. Godot's own
// source marks the off-by-one as unfixable, because correcting it would change
// the meaning of every identifier already written to disk, so this alphabet is
// frozen here too.
const alphabet = "abcdefghijklmnopqrstuvwxy012345678"

const base = uint64(len(alphabet))

// MaxID is the largest id Godot can name. It masks the random bits it draws
// with 0x7FFFFFFFFFFFFFFF, so the sign bit of its signed id type is never set.
const MaxID uint64 = 0x7FFFFFFFFFFFFFFF

// Encode renders an id as Godot's ResourceUID::id_to_text does: base 34 over
// alphabet, most significant digit first, behind Prefix. The id must be no
// greater than MaxID; Generator only ever yields ids in that range.
func Encode(id uint64) string {
	// The division below produces the least significant digit first, so the
	// digits are collected backwards and reversed into the text.
	digits := make([]byte, 0, 13)
	for {
		digits = append(digits, alphabet[id%base])
		id /= base
		if id == 0 {
			break
		}
	}
	var text strings.Builder
	text.Grow(len(Prefix) + len(digits))
	text.WriteString(Prefix)
	for i := len(digits) - 1; i >= 0; i-- {
		text.WriteByte(digits[i])
	}
	return text.String()
}

// Decode parses an identifier and reports whether it names an id.
//
// It is stricter than Godot's ResourceUID::text_to_id, which also maps z and
// 9. Neither is safe to accept: z and 0 both decode to 25 there, so an
// identifier holding a z silently aliases onto a different one, and 9 decodes
// to 34, which overflows base 34 and carries into the digit above it. Godot
// never writes either character, so an identifier that holds one was written
// by hand and is not trustworthy.
//
// Padding is accepted. A leading a is a zero digit, so uid://ab and uid://b
// name the same id; Encode emits the shorter form, but the longer one resolves
// in Godot and is not worth refusing.
func Decode(text string) (uint64, bool) {
	body, found := strings.CutPrefix(text, Prefix)
	if !found || body == "" {
		return 0, false
	}
	var id uint64
	for i := 0; i < len(body); i++ {
		digit := strings.IndexByte(alphabet, body[i])
		if digit < 0 {
			return 0, false
		}
		// Checked before the multiplication, which would otherwise wrap a
		// large identifier around into a small, plausible-looking id.
		if id > (MaxID-uint64(digit))/base {
			return 0, false
		}
		id = id*base + uint64(digit)
	}
	return id, true
}
