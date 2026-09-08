//go:build !no_gvisortapvsock

package gvisortapvsock

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/songgao/water"
)

type packetTap struct {
	io.ReadWriteCloser
	packets [][]byte
}

func (t *packetTap) Read(buf []byte) (int, error) {
	if len(t.packets) == 0 {
		return 0, io.EOF
	}
	n := copy(buf, t.packets[0])
	t.packets = t.packets[1:]
	return n, nil
}

type recordingConn struct {
	net.Conn
	data bytes.Buffer
}

func (c *recordingConn) Write(buf []byte) (int, error) { return c.data.Write(buf) }

func TestForwardTapToSocket(t *testing.T) {
	packets := [][]byte{
		bytes.Repeat([]byte{0x11}, 1500),
		bytes.Repeat([]byte{0x22}, 65535),
		[]byte("last packet"),
	}
	conn := &recordingConn{}
	d := &childDriver{
		tap:  &water.Interface{ReadWriteCloser: &packetTap{packets: packets}},
		conn: conn,
	}
	d.forwardTapToSocket()
	for i, want := range packets {
		var size [2]byte
		if _, err := io.ReadFull(&conn.data, size[:]); err != nil {
			t.Fatal(err)
		}
		n := int(binary.LittleEndian.Uint16(size[:]))
		if n != len(want) {
			t.Fatalf("packet %d length: got %d, want %d", i, n, len(want))
		}
		got := make([]byte, n)
		if _, err := io.ReadFull(&conn.data, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("packet %d corrupted", i)
		}
	}
	if conn.data.Len() != 0 {
		t.Fatal("unexpected trailing data")
	}
}
