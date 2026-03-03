package dmxcast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func dmx0(v byte) [512]byte {
	var d [512]byte
	d[0] = v
	return d
}

func TestUniverse_HTP_MaxAndFallback(t *testing.T) {
	u := NewUniverse(MergeHTP)

	u.Apply(1, 1, dmx0(5))
	require.Equal(t, byte(5), u.Merge()[0])

	u.Apply(2, 2, dmx0(10))
	require.Equal(t, byte(10), u.Merge()[0])

	// Source 2 fades to 0, should fall back to 5.
	u.Apply(2, 3, dmx0(0))
	require.Equal(t, byte(5), u.Merge()[0])

	// Remove source 1, should become 0.
	u.Remove(1)
	require.Equal(t, byte(0), u.Merge()[0])
}

func TestUniverse_LTP_LastWriterWins(t *testing.T) {
	u := NewUniverse(MergeLTP)

	u.Apply(1, 1, dmx0(10))
	require.Equal(t, byte(10), u.Merge()[0])

	// Later update wins (global seq).
	u.Apply(2, 2, dmx0(5))
	require.Equal(t, byte(5), u.Merge()[0])

	// Later update wins (global seq).
	u.Apply(1, 3, dmx0(9))
	require.Equal(t, byte(9), u.Merge()[0])
}

func TestUniverse_LTP_RemoveFallsBack(t *testing.T) {
	u := NewUniverse(MergeLTP)

	u.Apply(1, 1, dmx0(10))
	u.Apply(2, 2, dmx0(5))
	require.Equal(t, byte(5), u.Merge()[0])

	// Remove current winner (show 2), fall back to show 1.
	u.Remove(2)
	require.Equal(t, byte(10), u.Merge()[0])
}

func TestUniverse_SetMode_AffectsMerge(t *testing.T) {
	u := NewUniverse(MergeHTP)

	// HTP: max(10, 5) = 10
	u.Apply(1, 1, dmx0(10))
	u.Apply(2, 2, dmx0(5))
	require.Equal(t, byte(10), u.Merge()[0])

	// LTP: last update (seq=2) is show 2 -> 5
	u.SetMode(MergeLTP)
	require.Equal(t, byte(5), u.Merge()[0])

	// Back to HTP: max again -> 10
	u.SetMode(MergeHTP)
	require.Equal(t, byte(10), u.Merge()[0])
}

func TestUniverse_SourcesCount(t *testing.T) {
	u := NewUniverse(MergeHTP)
	require.Equal(t, 0, u.SourcesCount())

	u.Apply(1, 1, dmx0(1))
	u.Apply(2, 2, dmx0(2))
	require.Equal(t, 2, u.SourcesCount())

	u.Remove(1)
	require.Equal(t, 1, u.SourcesCount())
}
