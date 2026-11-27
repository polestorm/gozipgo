package zipwriter

import (
	"bytes"
	"errors"
	"hash/crc32"
	"testing"

	"github.com/polestorm/gozipgo/internal/deflate"
)

// checkData to dane do prób kontroli: tekst o małym słowniku i szum, którego strumień jest dłuższy niż
// checkReserve, więc dekoder pracuje już w trakcie zapisu.
func checkData() [][]byte {
	text := bytes.Repeat([]byte("<w:p><w:r><w:t>zażółć gęślą jaźń</w:t></w:r></w:p>\n"), 20000)
	noise := make([]byte, 3*checkReserve)
	x := uint32(1)
	for i := range noise {
		x = x*1664525 + 1013904223
		noise[i] = byte(x >> 24)
	}
	return [][]byte{nil, []byte("a"), text, noise, append(append([]byte(nil), noise...), text...)}
}

func deflated(t *testing.T, data []byte) []byte {
	var out bytes.Buffer
	w := deflate.NewWriter(&out)
	w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// verdict podaje kontroli strumień w kawałkach po chunk bajtów i zwraca jej wynik dla podanej treści.
func verdict(c *checker, stream []byte, chunk int, crc uint32, size uint64) bool {
	c.reset()
	for len(stream) > 0 {
		n := min(chunk, len(stream))
		c.feed(stream[:n])
		stream = stream[n:]
	}
	return c.finish(crc, size)
}

// TestSelfCheck: kontrola przyjmuje strumień kompresora dla jego treści, przy każdym podziale na zapisy,
// i odrzuca strumień zmieniony, ucięty albo dłuższy oraz treść o innym rozmiarze albo CRC-32. Dla strumienia
// dłuższego niż checkReserve dekoduje już w trakcie zapisu, nie dopiero na końcu.
// REQ: ZW-011
func TestSelfCheck(t *testing.T) {
	var c checker
	for i, data := range checkData() {
		stream := deflated(t, data)
		crc, size := crc32.ChecksumIEEE(data), uint64(len(data))
		for _, chunk := range []int{1, 4096, 32768, 65535, len(stream) + 1} {
			if chunk == 1 && len(stream) > 100000 {
				continue
			}
			if !verdict(&c, stream, chunk, crc, size) {
				t.Errorf("data %d, chunks of %d: the stream of the compressor is rejected (%v)", i, chunk, c.err)
			}
		}
		if len(stream) > 2*checkReserve {
			c.reset()
			c.feed(stream)
			if c.size == 0 {
				t.Errorf("data %d: nothing is decoded before the end of a stream of %d bytes", i, len(stream))
			}
		}
		if verdict(&c, stream, 4096, crc^1, size) {
			t.Errorf("data %d: another CRC-32 is accepted", i)
		}
		if verdict(&c, stream, 4096, crc, size+1) {
			t.Errorf("data %d: another size is accepted", i)
		}
		if verdict(&c, stream[:len(stream)-1], 4096, crc, size) {
			t.Errorf("data %d: a stream without its last byte is accepted", i)
		}
		if verdict(&c, append(append([]byte(nil), stream...), 0), 4096, crc, size) {
			t.Errorf("data %d: a stream with a byte after its end is accepted", i)
		}
		// jeden zmieniony bit: w nagłówku pierwszego bloku i w kilku miejscach danych (ostatni bajt ma bity
		// dopełnienia, które do strumienia nie należą, więc go pomijamy)
		for _, at := range []int{0, len(stream) / 3, len(stream) / 2, len(stream) - 2} {
			if at < 0 || at >= len(stream)-1 {
				continue
			}
			bad := append([]byte(nil), stream...)
			bad[at] ^= 1
			if verdict(&c, bad, 4096, crc, size) {
				t.Errorf("data %d: a stream with a bit changed in byte %d is accepted", i, at)
			}
		}
	}
}

// TestSelfCheckFailure: gdy treść odczytana ze strumienia kompresora nie jest treścią wpisu, zamknięcie wpisu
// kończy się write-self-check i archiwum nie da się dokończyć. Kompresor działa poprawnie, więc niezgodność
// wywołujemy, zmieniając sumę treści, którą wpis policzył.
// REQ: ZW-011
func TestSelfCheckFailure(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	e, err := w.Create(Header{Name: "a.txt", Method: Deflate})
	if err != nil {
		t.Fatal(err)
	}
	e.Write([]byte("content"))
	e.(*entryWriter).crc ^= 1
	var werr *Error
	if err := e.Close(); !errors.As(err, &werr) || werr.Code != CodeSelfCheck || werr.Entry != 0 || werr.Name != "a.txt" {
		t.Fatalf("Close of the entry: %v, want %s for entry 0", err, CodeSelfCheck)
	}
	if err := w.Add(Header{Name: "b.txt"}, nil); !errors.As(err, &werr) || werr.Code != CodeSelfCheck {
		t.Errorf("Add after the failure: %v, want the same error", err)
	}
	if err := w.Close(); !errors.As(err, &werr) || werr.Code != CodeSelfCheck {
		t.Errorf("Close after the failure: %v, want the same error", err)
	}

	// Stored nie ma strumienia do sprawdzenia: ta sama zmiana nie daje błędu kontroli.
	w = New(&buf)
	e, _ = w.Create(Header{Name: "a.txt"})
	e.Write([]byte("content"))
	e.(*entryWriter).crc ^= 1
	if err := e.Close(); err != nil {
		t.Errorf("Stored entry: %v", err)
	}
}

// TestSkipSelfCheck: z Options.SkipSelfCheck pisarz nie dekoduje własnych strumieni (ta sama zmieniona suma nie
// daje write-self-check), a bajty archiwum są te same co z kontrolą, dla Add i dla Create.
// REQ: ZW-011
func TestSkipSelfCheck(t *testing.T) {
	var buf bytes.Buffer
	w := NewWithOptions(&buf, Options{SkipSelfCheck: true})
	e, err := w.Create(Header{Name: "a.txt", Method: Deflate})
	if err != nil {
		t.Fatal(err)
	}
	e.Write([]byte("content"))
	e.(*entryWriter).crc ^= 1
	if err := e.Close(); err != nil {
		t.Errorf("Close of the entry without the check: %v", err)
	}
	if w.check.inf != nil {
		t.Error("the decoder of the check was set up although the check is off")
	}

	archive := func(opts Options) []byte {
		var out bytes.Buffer
		w := NewWithOptions(&out, opts)
		for i, data := range checkData() {
			if err := w.Add(Header{Name: string(rune('a' + i)), Method: Deflate}, data); err != nil {
				t.Fatal(err)
			}
			e, err := w.Create(Header{Name: string(rune('A' + i)), Method: Deflate})
			if err != nil {
				t.Fatal(err)
			}
			e.Write(data)
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	if !bytes.Equal(archive(Options{}), archive(Options{SkipSelfCheck: true})) {
		t.Error("the bytes of the archive depend on SkipSelfCheck")
	}
}
