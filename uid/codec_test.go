package uid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The expected texts come from Godot's own encoding. uid://d4n4ub6itg400 for
// MaxID is the value quoted in the comment beside ResourceUID::id_to_text.
func TestEncodeMatchesGodot(t *testing.T) {
	cases := []struct {
		id   uint64
		text string
	}{
		{0, "uid://a"},
		{1, "uid://b"},
		{24, "uid://y"},
		{25, "uid://0"},
		{33, "uid://8"},
		{34, "uid://ba"},
		{35, "uid://bb"},
		{MaxID, "uid://d4n4ub6itg400"},
	}
	for _, test := range cases {
		assert.Equal(t, test.text, Encode(test.id))
		id, ok := Decode(test.text)
		assert.True(t, ok)
		assert.Equal(t, test.id, id)
	}
}

func TestEncodeNeverUsesTheMissingCharacters(t *testing.T) {
	// z and 9 are outside Godot's alphabet. Walking a stride that is coprime
	// with the base covers every digit position.
	for id := uint64(0); id < 500000; id += 7 {
		for _, character := range Encode(id) {
			require.NotEqual(t, 'z', character)
			require.NotEqual(t, '9', character)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	ids := []uint64{0, 1, 33, 34, 1 << 20, 1 << 40, MaxID - 1, MaxID}
	for _, id := range ids {
		decoded, ok := Decode(Encode(id))
		assert.True(t, ok)
		assert.Equal(t, id, decoded)
	}
}

func TestDecodeRejectsInvalidText(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"no prefix":           "abc",
		"partial prefix":      "uid:/b",
		"empty body":          "uid://",
		"godot invalid":       "uid://<invalid>",
		"z is not a digit":    "uid://az",
		"nine is not a digit": "uid://a9",
		"uppercase":           "uid://A",
		"punctuation":         "uid://a-b",
		"path instead of id":  "res://player.gd",
		"above max":           "uid://d4n4ub6itg401",
		// Long enough to wrap a uint64 if the overflow check were missing.
		"far above max": "uid://bbbbbbbbbbbbbbbbbbbb",
	}
	for name, text := range cases {
		_, ok := Decode(text)
		assert.False(t, ok, name)
	}
}

func TestDecodeAcceptsPadding(t *testing.T) {
	id, ok := Decode("uid://aaab")
	require.True(t, ok)
	require.Equal(t, uint64(1), id)
}
