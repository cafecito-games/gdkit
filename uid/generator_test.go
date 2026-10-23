package uid

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratorStaysInGodotsRange(t *testing.T) {
	// A reader of all ones would hand back an id with every bit set; the
	// generator must clear the sign bit, as Godot's create_id does.
	generator := NewGenerator(onesReader{})
	id, err := generator.Next()
	require.NoError(t, err)
	require.Equal(t, MaxID, id)
}

func TestGeneratorSkipsReservedIdentifiers(t *testing.T) {
	generator := seeded(1)
	generator.Reserve(1)
	generator.Reserve(2)
	id, err := generator.Next()
	require.NoError(t, err)
	require.Equal(t, uint64(3), id)
}

func TestGeneratorNeverRepeatsItself(t *testing.T) {
	generator := NewGenerator(nil)
	seen := make(map[uint64]struct{}, 1000)
	for range 1000 {
		id, err := generator.Next()
		require.NoError(t, err)
		_, repeated := seen[id]
		require.False(t, repeated)
		seen[id] = struct{}{}
	}
}

func TestGeneratorReportsAFailedDraw(t *testing.T) {
	_, err := NewGenerator(failingReader{}).Next()
	require.Error(t, err)
	require.ErrorIs(t, err, errNoRandomness)
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
