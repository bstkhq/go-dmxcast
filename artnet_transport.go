package dmxcast

import (
	"net"

	"github.com/jsimonetti/go-artnet/packet"
)

type ArtNetConfig struct {
	// DstIP is the destination IP (unicast).
	DstIP net.IP
	// SrcIP is the optional local bind IP (nil = OS default).
	SrcIP  net.IP
	SubUni uint8
	Net    uint8
}

type ArtNetTransport struct {
	cfg *ArtNetConfig

	conn    *net.UDPConn
	dstAddr *net.UDPAddr
	seq     uint8
}

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

func (t *ArtNetTransport) Close() error {
	return t.conn.Close()
}
