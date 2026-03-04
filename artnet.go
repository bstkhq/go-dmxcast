package dmxcast

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/jsimonetti/go-artnet/packet"
)

// ArtNetConfig configures an ArtNetTransport.
//
// The transport sends Art-Net ArtDMX packets via UDP unicast to DstIP:6454.
// Net and SubUni select the target Art-Net universe (as defined by the Art-Net
// specification).
//
// If SrcIP is provided, the UDP socket is bound to SrcIP:6454 (useful on
// multi-homed hosts). If SrcIP is nil, the OS default routing and an ephemeral
// source port are used.
type ArtNetConfig struct {
	// DstIP is the destination IP for UDP unicast Art-Net traffic.
	DstIP net.IP
	// SrcIP is the optional local bind IP (nil = OS default).
	SrcIP net.IP
	// SubUni is the Art-Net SubUni (0..255) field used in ArtDMX packets.
	SubUni uint8
	// Net is the Art-Net Net (0..127) field used in ArtDMX packets.
	Net uint8
}

// ArtNetTransport sends DMX frames as Art-Net ArtDMX packets over UDP.
type ArtNetTransport struct {
	cfg *ArtNetConfig

	conn    *net.UDPConn
	dstAddr *net.UDPAddr
	seq     uint8
}

// NewArtNetTransport creates a UDP unicast Art-Net sender.
//
// The returned transport maintains an internal ArtDMX sequence counter that
// increments on each Send. Sequence 0 is skipped (wraps from 255 back to 1).
func NewArtNetTransport(cfg *ArtNetConfig) (*ArtNetTransport, error) {
	dstAddr := &net.UDPAddr{IP: cfg.DstIP, Port: int(packet.ArtNetPort)}

	var (
		conn *net.UDPConn
		err  error
	)
	if cfg.SrcIP != nil && cfg.SrcIP.To4() != nil {
		localAddr := &net.UDPAddr{IP: cfg.SrcIP, Port: int(packet.ArtNetPort)}
		conn, err = net.ListenUDP("udp", localAddr)
	} else {
		conn, err = net.ListenUDP("udp", nil)
	}
	if err != nil {
		return nil, err
	}

	return &ArtNetTransport{
		cfg:     cfg,
		conn:    conn,
		dstAddr: dstAddr,
		seq:     1,
	}, nil
}

// Send encodes the given DMX frame as an Art-Net ArtDMX packet and sends it via UDP.
func (t *ArtNetTransport) Send(dmx [512]byte) error {
	pkt := &packet.ArtDMXPacket{
		Sequence: t.seq,
		SubUni:   t.cfg.SubUni,
		Net:      t.cfg.Net,
		Data:     dmx,
	}

	b, err := pkt.MarshalBinary()
	if err != nil {
		return err
	}

	if _, err := t.conn.WriteToUDP(b, t.dstAddr); err != nil {
		return err
	}

	t.seq++
	if t.seq == 0 {
		t.seq = 1
	}
	return nil
}

// Close closes the underlying UDP socket.
func (t *ArtNetTransport) Close() error {
	return t.conn.Close()
}

// ArtNetListener listens for Art-Net ArtDMX packets over UDP and emits frames.
type ArtNetListener struct {
	conn *net.UDPConn
	buf  []byte
}

// ArtNetListenerConfig configures ArtNetListener.
type ArtNetListenerConfig struct {
	// BindIP is the optional local bind IP. Nil means all interfaces.
	BindIP net.IP
	// ReadBuffer sets the UDP socket read buffer (0 = OS default).
	ReadBuffer int
}

// NewArtNetListener binds UDP/6454 and returns an ArtNetListener.
func NewArtNetListener(cfg ArtNetListenerConfig) (*ArtNetListener, error) {
	laddr := &net.UDPAddr{IP: cfg.BindIP, Port: int(packet.ArtNetPort)}
	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return nil, err
	}

	if cfg.ReadBuffer > 0 {
		_ = conn.SetReadBuffer(cfg.ReadBuffer)
	}

	return &ArtNetListener{
		conn: conn,
		buf:  make([]byte, 1500),
	}, nil
}

// Close closes the UDP socket.
func (l *ArtNetListener) Close() error { return l.conn.Close() }

// Run reads ArtDMX packets until ctx is done and calls h for each received packet.
func (l *ArtNetListener) Run(ctx context.Context, h FrameHandler) error {
	for {
		_ = l.conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, _, err := l.conn.ReadFromUDP(l.buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					return nil
				default:
					continue
				}
			}

			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			return err
		}

		var p packet.ArtDMXPacket
		if err := p.UnmarshalBinary(l.buf[:n]); err != nil {
			continue
		}

		u := (uint16(p.Net) << 8) | uint16(p.SubUni)
		if err := h(time.Now(), u, p.Data); err != nil {
			return err
		}
	}
}
