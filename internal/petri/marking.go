package petri

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Marking is a concrete token vector indexed by declared place order. Two
// markings with different token counts are always distinct states.
type Marking []int

// Canonical renders a deterministic, collision-free textual key. It never
// merges markings that differ in tokens.
func (m Marking) Canonical() string {
	parts := make([]string, len(m))
	for i, v := range m {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ".")
}

// StableID derives a stable, content-based identity that survives export,
// database deletion and re-import.
func (m Marking) StableID() string {
	sum := sha256.Sum256([]byte("marking:" + m.Canonical()))
	return "M-" + hex.EncodeToString(sum[:8])
}

// Clone returns an independent copy.
func (m Marking) Clone() Marking {
	c := make(Marking, len(m))
	copy(c, m)
	return c
}

// Equal compares full token vectors.
func (m Marking) Equal(o Marking) bool {
	if len(m) != len(o) {
		return false
	}
	for i := range m {
		if m[i] != o[i] {
			return false
		}
	}
	return true
}

// Covers reports componentwise domination: m >= o in every place.
func (m Marking) Covers(o Marking) bool {
	if len(m) != len(o) {
		return false
	}
	for i := range m {
		if m[i] < o[i] {
			return false
		}
	}
	return true
}

// ByPlace projects the vector onto stable place IDs for display.
func (m Marking) ByPlace(n *Net) map[string]int {
	out := map[string]int{}
	for i, p := range n.Places {
		if i < len(m) {
			out[p.ID] = m[i]
		}
	}
	return out
}
