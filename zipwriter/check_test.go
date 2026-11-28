package zipwriter

import (
	"bytes"
	"errors"
	"io"
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

// verdict podaje kontroli treść i strumień w kawałkach po chunk bajtów i zwraca jej wynik. Treść idzie przez
// expect (jak w Add) albo, gdy streamed, przez content przed strumieniem (jak we wpisie z Create).
func verdict(c *checker, stream []byte, chunk int, content []byte, streamed bool) bool {
	c.reset()
	if streamed {
		for d := content; len(d) > 0; {
			n := min(4096, len(d))
			c.content(d[:n])
			d = d[n:]
		}
	} else {
		c.expect(content)
	}
	for len(stream) > 0 {
		n := min(chunk, len(stream))
		c.feed(stream[:n])
		stream = stream[n:]
	}
	return c.finish()
}

// TestSelfCheck: kontrola przyjmuje strumień kompresora dla jego treści, przy każdym podziale na zapisy,
// i odrzuca strumień zmieniony, ucięty albo dłuższy oraz treść różną w jednym bajcie, krótszą albo dłuższą;
// tak samo dla treści podanej w całości (Add) i w kawałkach (Create). Porównywane są bajty, więc treść o tym
// samym rozmiarze różna w jednym bajcie jest odrzucana bez względu na jakąkolwiek sumę. Dla strumienia
// dłuższego niż checkReserve kontrola dekoduje już w trakcie zapisu, nie dopiero na końcu.
// REQ: ZW-011
func TestSelfCheck(t *testing.T) {
	var c checker
	for i, data := range checkData() {
		stream := deflated(t, data)
		for _, streamed := range []bool{false, true} {
			for _, chunk := range []int{1, 4096, 32768, 65535, len(stream) + 1} {
				if chunk == 1 && len(stream) > 100000 {
					continue
				}
				if !verdict(&c, stream, chunk, data, streamed) {
					t.Errorf("data %d, streamed %v, chunks of %d: the stream of the compressor is rejected (%v)", i, streamed, chunk, c.err)
				}
			}
			if len(stream) > 2*checkReserve {
				c.reset()
				c.expect(data)
				c.feed(stream)
				if c.rp == 0 {
					t.Errorf("data %d: nothing is decoded before the end of a stream of %d bytes", i, len(stream))
				}
			}
			for _, at := range []int{0, len(data) / 2, len(data) - 1} {
				if at < 0 || at >= len(data) {
					continue
				}
				other := append([]byte(nil), data...)
				other[at] ^= 1
				if verdict(&c, stream, 4096, other, streamed) {
					t.Errorf("data %d, streamed %v: content that differs in byte %d is accepted", i, streamed, at)
				}
			}
			if len(data) > 0 && verdict(&c, stream, 4096, data[:len(data)-1], streamed) {
				t.Errorf("data %d, streamed %v: shorter content is accepted", i, streamed)
			}
			if verdict(&c, stream, 4096, append(append([]byte(nil), data...), 0), streamed) {
				t.Errorf("data %d, streamed %v: longer content is accepted", i, streamed)
			}
			if verdict(&c, stream[:len(stream)-1], 4096, data, streamed) {
				t.Errorf("data %d, streamed %v: a stream without its last byte is accepted", i, streamed)
			}
			if verdict(&c, append(append([]byte(nil), stream...), 0), 4096, data, streamed) {
				t.Errorf("data %d, streamed %v: a stream with a byte after its end is accepted", i, streamed)
			}
			// jeden zmieniony bit: w nagłówku pierwszego bloku i w kilku miejscach danych (ostatni bajt ma bity
			// dopełnienia, które do strumienia nie należą, więc go pomijamy)
			for _, at := range []int{0, len(stream) / 3, len(stream) / 2, len(stream) - 2} {
				if at < 0 || at >= len(stream)-1 {
					continue
				}
				bad := append([]byte(nil), stream...)
				bad[at] ^= 1
				if verdict(&c, bad, 4096, data, streamed) {
					t.Errorf("data %d, streamed %v: a stream with a bit changed in byte %d is accepted", i, streamed, at)
				}
			}
		}
	}
}

// TestSelfCheckQueue: kolejka treści wpisu strumieniowego nie rośnie z rozmiarem wpisu, gdy strumień jest
// dekodowany na bieżąco (szum: strumień dłuższy niż treść), a po wpisie, który ją rozdął (zera), kontrola
// oddaje pamięć przy następnym wpisie.
// REQ: ZW-011
func TestSelfCheckQueue(t *testing.T) {
	noise := make([]byte, 16<<20)
	x := uint32(7)
	for i := range noise {
		x = x*1664525 + 1013904223
		noise[i] = byte(x >> 24)
	}
	w := New(io.Discard)
	write := func(name string, data []byte) {
		e, err := w.Create(Header{Name: name, Method: Deflate})
		if err != nil {
			t.Fatal(err)
		}
		for d := data; len(d) > 0; {
			n := min(32<<10, len(d))
			if _, err := e.Write(d[:n]); err != nil {
				t.Fatal(err)
			}
			d = d[n:]
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("noise", noise)
	if got := cap(w.check.pend); got > 4<<20 {
		t.Errorf("the queue of content reached %d bytes for %d bytes of noise", got, len(noise))
	}
	write("zeros", make([]byte, 16<<20))
	if got := cap(w.check.pend); got < 8<<20 {
		t.Fatalf("the queue has %d bytes after 16 MiB of zeros: the test does not reach the case it is for", got)
	}
	write("small", []byte("content"))
	if got := cap(w.check.pend); got > pendKeep {
		t.Errorf("the queue keeps %d bytes after the next entry, want at most %d", got, pendKeep)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestSelfCheckFailure: gdy treść odczytana ze strumienia kompresora nie jest treścią wpisu, zamknięcie wpisu
// kończy się write-self-check i archiwum nie da się dokończyć. Kompresor działa poprawnie, więc niezgodność
// wywołujemy, zmieniając bajt w kopii treści, którą trzyma kontrola.
// REQ: ZW-011
func TestSelfCheckFailure(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	e, err := w.Create(Header{Name: "a.txt", Method: Deflate})
	if err != nil {
		t.Fatal(err)
	}
	e.Write([]byte("content"))
	w.check.pend[0] ^= 1
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

	// Stored nie ma strumienia do sprawdzenia: kontrola nie trzyma jego treści.
	w = New(&buf)
	e, _ = w.Create(Header{Name: "a.txt"})
	e.Write([]byte("content"))
	if len(w.check.pend) != 0 {
		t.Error("the check keeps the content of a Stored entry")
	}
	if err := e.Close(); err != nil {
		t.Errorf("Stored entry: %v", err)
	}
}

// TestSkipSelfCheck: z Options.SkipSelfCheck pisarz nie dekoduje własnych strumieni i nie trzyma kopii treści,
// a bajty archiwum są te same co z kontrolą, dla Add i dla Create.
// REQ: ZW-011
func TestSkipSelfCheck(t *testing.T) {
	var buf bytes.Buffer
	w := NewWithOptions(&buf, Options{SkipSelfCheck: true})
	e, err := w.Create(Header{Name: "a.txt", Method: Deflate})
	if err != nil {
		t.Fatal(err)
	}
	e.Write([]byte("content"))
	if err := e.Close(); err != nil {
		t.Errorf("Close of the entry without the check: %v", err)
	}
	if len(w.check.pend) != 0 || w.check.inf != nil {
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
