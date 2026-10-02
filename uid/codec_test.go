package uid

import "testing"

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
		if got := Encode(test.id); got != test.text {
			t.Errorf("Encode(%d) = %q, want %q", test.id, got, test.text)
		}
		id, ok := Decode(test.text)
		if !ok || id != test.id {
			t.Errorf("Decode(%q) = %d, %t, want %d, true", test.text, id, ok, test.id)
		}
	}
}

func TestEncodeNeverUsesTheMissingCharacters(t *testing.T) {
	// z and 9 are outside Godot's alphabet. Walking a stride that is coprime
	// with the base covers every digit position.
	for id := uint64(0); id < 500000; id += 7 {
		for _, character := range Encode(id) {
			if character == 'z' || character == '9' {
				t.Fatalf("Encode(%d) = %q uses %q", id, Encode(id), character)
			}
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	ids := []uint64{0, 1, 33, 34, 1 << 20, 1 << 40, MaxID - 1, MaxID}
	for _, id := range ids {
		decoded, ok := Decode(Encode(id))
		if !ok || decoded != id {
			t.Errorf("round trip of %d gave %d, %t", id, decoded, ok)
		}
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
		if id, ok := Decode(text); ok {
			t.Errorf("%s: Decode(%q) = %d, true, want false", name, text, id)
		}
	}
}

func TestDecodeAcceptsPadding(t *testing.T) {
	id, ok := Decode("uid://aaab")
	if !ok || id != 1 {
		t.Fatalf("Decode(\"uid://aaab\") = %d, %t, want 1, true", id, ok)
	}
}
