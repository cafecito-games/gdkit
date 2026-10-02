package uid

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// Generator issues identifiers the way Godot's ResourceUID::create_id does: 63
// random bits, redrawn whenever the draw lands on an identifier the project
// already uses. An id is not derived from the path or the contents of the file
// it names, so two runs over the same project produce different identifiers.
type Generator struct {
	random io.Reader
	taken  map[uint64]struct{}
}

// NewGenerator returns a generator drawing from random, or from
// crypto/rand.Reader when random is nil. Tests pass a fixed reader to get
// predictable identifiers.
func NewGenerator(random io.Reader) *Generator {
	if random == nil {
		random = rand.Reader
	}
	return &Generator{random: random, taken: make(map[uint64]struct{})}
}

// Reserve records an identifier as being in use, so Next will not return it.
// Callers reserve every identifier already present in the project before
// generating, including those of files gdkit does not otherwise examine.
func (g *Generator) Reserve(id uint64) { g.taken[id] = struct{}{} }

// Next returns an unused identifier and reserves it. It fails only when the
// source of randomness does.
func (g *Generator) Next() (uint64, error) {
	var bytes [8]byte
	for {
		if _, err := io.ReadFull(g.random, bytes[:]); err != nil {
			return 0, fmt.Errorf("draw a uid: %w", err)
		}
		id := binary.LittleEndian.Uint64(bytes[:]) & MaxID
		if _, exists := g.taken[id]; exists {
			continue
		}
		g.Reserve(id)
		return id, nil
	}
}
