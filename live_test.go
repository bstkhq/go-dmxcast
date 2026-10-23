package dmxcast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUniverse_LiveHoldsItsChannelsOverTheShows(t *testing.T) {
	u := NewUniverse(MergeHTP)
	show := dmx0(200)
	show[1] = 50
	u.Apply(1, 1, show)

	var dmx [512]byte
	var hold [512]bool
	dmx[0], hold[0] = 10, true // lower than the show, still wins
	u.ApplyLive(9, 2, dmx, hold)

	out := u.Merge()
	require.Equal(t, byte(10), out[0], "a held channel is the live value")
	require.Equal(t, byte(50), out[1], "a channel the live source doesn't hold stays the show's")

	u.RemoveLive(9)
	require.Equal(t, byte(200), u.Merge()[0], "releasing hands the channel back to the show")
}

func TestUniverse_LatestLiveWinsASharedChannel(t *testing.T) {
	u := NewUniverse(MergeHTP)
	var a, b [512]byte
	var hold [512]bool
	hold[3] = true
	a[3], b[3] = 1, 2
	u.ApplyLive(1, 5, a, hold)
	u.ApplyLive(2, 6, b, hold)
	require.Equal(t, byte(2), u.Merge()[3])
	u.ApplyLive(1, 7, a, hold)
	require.Equal(t, byte(1), u.Merge()[3])
}
