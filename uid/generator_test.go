package uid

import (
	"errors"
	"io"
	"testing"
)

func TestGeneratorStaysInGodotsRange(t *testing.T) {
	// A reader of all ones would hand back an id with every bit set; the
	// generator must clear the sign bit, as Godot's create_id does.
	generator := NewGenerator(onesReader{})
	id, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if id != MaxID {
		t.Fatalf("id = %x, want %x", id, MaxID)
	}
}

func TestGeneratorSkipsReservedIdentifiers(t *testing.T) {
	generator := seeded(1)
	generator.Reserve(1)
	generator.Reserve(2)
	id, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if id != 3 {
		t.Fatalf("id = %d, want 3", id)
	}
}

func TestGeneratorNeverRepeatsItself(t *testing.T) {
	generator := NewGenerator(nil)
	seen := make(map[uint64]struct{}, 1000)
	for range 1000 {
		id, err := generator.Next()
		if err != nil {
			t.Fatal(err)
		}
		if _, repeated := seen[id]; repeated {
			t.Fatalf("id %d was issued twice", id)
		}
		seen[id] = struct{}{}
	}
}

func TestGeneratorReportsAFailedDraw(t *testing.T) {
	_, err := NewGenerator(failingReader{}).Next()
	if err == nil {
		t.Fatal("Next succeeded, want the reader's failure")
	}
	if !errors.Is(err, errNoRandomness) {
		t.Fatalf("error = %v, want it to wrap the reader's", err)
	}
}

type onesReader struct{}

func (onesReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0xFF
	}
	return len(p), nil
}

var errNoRandomness = errors.New("no randomness here")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errNoRandomness }

var _ io.Reader = failingReader{}
