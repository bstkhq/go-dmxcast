package dmxcast

import (
	"net"
	"testing"
	"time"

	"github.com/jsimonetti/go-artnet/packet"
	"github.com/stretchr/testify/require"
)

func listenUDP1270(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	return c
}

func recvArtDMX(t *testing.T, c *net.UDPConn) packet.ArtDMXPacket {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))

	buf := make([]byte, 2048)
	n, _, err := c.ReadFromUDP(buf)
	require.NoError(t, err)

	var p packet.ArtDMXPacket
	require.NoError(t, p.UnmarshalBinary(buf[:n]))
	return p
}

func TestFixedArtNetMap(t *testing.T) {
	m := FixedArtNetMap(7, 204)
	netID, subUni := m(1234)
	require.Equal(t, uint8(7), netID)
	require.Equal(t, uint8(204), subUni)
}

func TestArtNetTransport_Send_UsesMapAndSendsPacket(t *testing.T) {
	rx := listenUDP1270(t)
	defer rx.Close()

	txConn := listenUDP1270(t)
	defer txConn.Close()

	called := struct {
		ok       bool
		universe uint16
	}{}

	cfg := &ArtNetConfig{
		DstIP: net.ParseIP("127.0.0.1"),
		Map: func(u uint16) (uint8, uint8) {
			called.ok = true
			called.universe = u
			return 0, 204
		},
	}

	dst := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rx.LocalAddr().(*net.UDPAddr).Port}
	tx := newArtNetTransportConn(cfg, txConn, dst)

	var dmx [512]byte
	dmx[0] = 1
	dmx[1] = 2
	dmx[2] = 3
	dmx[511] = 255

	require.NoError(t, tx.Send(42, dmx))

	p := recvArtDMX(t, rx)

	require.True(t, called.ok)
	require.Equal(t, uint16(42), called.universe)

	require.Equal(t, uint8(1), p.Sequence)
	require.Equal(t, uint8(204), p.SubUni)
	require.Equal(t, uint8(0), p.Net)
	require.Equal(t, dmx, p.Data)
}

func TestArtNetTransport_Sequence_WrapsSkippingZero(t *testing.T) {
	rx := listenUDP1270(t)
	defer rx.Close()

	txConn := listenUDP1270(t)
	defer txConn.Close()

	cfg := &ArtNetConfig{
		DstIP: net.ParseIP("127.0.0.1"),
		Map:   FixedArtNetMap(0, 1),
	}

	dst := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rx.LocalAddr().(*net.UDPAddr).Port}
	tx := newArtNetTransportConn(cfg, txConn, dst)

	var dmx [512]byte
	dmx[0] = 123

	// Drive sequence wrap: 255 increments -> wraps to 0 internally, then skip to 1.
	// We check the first send and the 256th send.
	var firstSeq, lastSeq uint8

	for i := range 256 {
		require.NoError(t, tx.Send(1, dmx))
		p := recvArtDMX(t, rx)
		if i == 0 {
			firstSeq = p.Sequence
		}
		if i == 255 {
			lastSeq = p.Sequence
		}
	}

	require.Equal(t, uint8(1), firstSeq)
	require.Equal(t, uint8(1), lastSeq)
}

// newArtNetTransportConn is for tests: it uses an existing UDPConn.
// dstAddr.Port can be set to something other than ArtNetPort.
func newArtNetTransportConn(cfg *ArtNetConfig, conn *net.UDPConn, dstAddr *net.UDPAddr) *ArtNetTransport {
	return &ArtNetTransport{
		cfg:     cfg,
		conn:    conn,
		dstAddr: dstAddr,
		seq:     1,
	}
}
