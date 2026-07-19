// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
	"unicode/utf8"
)

// fuzzConn feeds a fixed byte stream to Conn.Read; writes are discarded.
type fuzzConn struct {
	net.Conn
	r *bytes.Reader
}

func (c fuzzConn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c fuzzConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c fuzzConn) Close() error                       { return nil }
func (c fuzzConn) SetDeadline(t time.Time) error      { return nil }
func (c fuzzConn) SetReadDeadline(t time.Time) error  { return nil }
func (c fuzzConn) SetWriteDeadline(t time.Time) error { return nil }

// frame builds one wire frame for seeds.
func frame(kind byte, payload []byte) []byte {
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(payload)+1))
	hdr[4] = kind
	return append(hdr[:], payload...)
}

// FuzzConnRead drives the frame parser with an arbitrary peer byte stream.
func FuzzConnRead(f *testing.F) {
	f.Add(frame(KindOutput, []byte{3, 'h', 'i'}))
	f.Add(frame(KindEvent, []byte(`{"kind":"hello","proto":1,"version":"dev"}`)))
	f.Add(frame(KindEvent, []byte(`{"kind":"`)))
	f.Add(frame('x', []byte("nope")))
	f.Add(frame(KindOutput, nil))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 'o'})
	f.Add([]byte{0, 0, 0})
	f.Add(bytes.Repeat(frame(KindOutput, []byte{0, 'a'}), 3))
	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewConn(fuzzConn{r: bytes.NewReader(data)})
		for {
			kind, idx, _, _, err := c.Read()
			if err != nil {
				return
			}
			if kind != KindOutput && kind != KindEvent {
				t.Fatalf("Read returned unknown kind %q without error", kind)
			}
			if idx < 0 || idx > 255 {
				t.Fatalf("output idx %d out of frame-contract range", idx)
			}
		}
	})
}

// FuzzRoundTrip checks write->read symmetry for both frame kinds.
func FuzzRoundTrip(f *testing.F) {
	f.Add(0, []byte("chunk"), "delegate", "task text")
	f.Add(255, []byte{}, "", "")
	f.Add(7, []byte{0x1b, ']', '1', '1'}, "hello", "\x00\xff†")
	f.Fuzz(func(t *testing.T, idx int, chunk []byte, kind, task string) {
		idx &= 0xff
		var buf bytes.Buffer
		w := NewConn(fuzzConn{r: bytes.NewReader(nil)})
		w.c = writerConn{&buf}
		if err := w.WriteOutput(idx, chunk); err != nil {
			t.Fatal(err)
		}
		ev := Event{Kind: kind, Board: []Task{{Task: task}}}
		if err := w.WriteEvent(ev); err != nil {
			t.Fatal(err)
		}
		r := NewConn(fuzzConn{r: bytes.NewReader(buf.Bytes())})
		k, gotIdx, gotChunk, _, err := r.Read()
		if err != nil || k != KindOutput || gotIdx != idx || !bytes.Equal(gotChunk, chunk) {
			t.Fatalf("output round trip: kind=%q idx=%d chunk=%q err=%v", k, gotIdx, gotChunk, err)
		}
		k, _, _, gotEv, err := r.Read()
		if err != nil || k != KindEvent {
			t.Fatalf("event round trip: kind=%q ev=%+v err=%v", k, gotEv, err)
		}
		// json.Marshal replaces invalid UTF-8 with U+FFFD; exact equality holds for valid strings only
		if utf8.ValidString(kind) && gotEv.Kind != kind {
			t.Fatalf("event kind round trip: %q != %q", gotEv.Kind, kind)
		}
		if len(gotEv.Board) != 1 || (utf8.ValidString(task) && gotEv.Board[0].Task != task) {
			t.Fatalf("event board round trip: %+v", gotEv.Board)
		}
	})
}

// writerConn captures writes for round-trip fuzzing.
type writerConn struct{ b *bytes.Buffer }

func (c writerConn) Read(p []byte) (int, error)         { return 0, nil }
func (c writerConn) Write(p []byte) (int, error)        { return c.b.Write(p) }
func (c writerConn) Close() error                       { return nil }
func (c writerConn) LocalAddr() net.Addr                { return nil }
func (c writerConn) RemoteAddr() net.Addr               { return nil }
func (c writerConn) SetDeadline(t time.Time) error      { return nil }
func (c writerConn) SetReadDeadline(t time.Time) error  { return nil }
func (c writerConn) SetWriteDeadline(t time.Time) error { return nil }
