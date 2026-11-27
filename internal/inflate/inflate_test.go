package inflate

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// bitWriter składa strumień DEFLATE bit po bicie (najwcześniejszy bit najniżej w bajcie, RFC 1951, 3.1.1).
type bitWriter struct {
	out []byte
	n   uint
}

// bits dopisuje n bitów wartości v, od najmniej znaczącego.
func (w *bitWriter) bits(v uint32, n uint) *bitWriter {
	for i := uint(0); i < n; i++ {
		if w.n%8 == 0 {
			w.out = append(w.out, 0)
		}
		w.out[len(w.out)-1] |= byte(v>>i&1) << (w.n % 8)
		w.n++
	}
	return w
}

// code dopisuje kod Huffmana: od bitu najbardziej znaczącego.
func (w *bitWriter) code(c uint32, n uint) *bitWriter {
	for i := int(n) - 1; i >= 0; i-- {
		w.bits(c>>uint(i)&1, 1)
	}
	return w
}

// fixedLitCode zwraca kod stały symbolu (tabela z RFC 1951, 3.2.6).
func fixedLitCode(s int) (uint32, uint) {
	switch {
	case s < 144:
		return uint32(0x30 + s), 8
	case s < 256:
		return uint32(0x190 + s - 144), 9
	case s < 280:
		return uint32(s - 256), 7
	}
	return uint32(0xC0 + s - 280), 8
}

func (w *bitWriter) lit(s int) *bitWriter { c, n := fixedLitCode(s); return w.code(c, n) }

// sample to dane deterministyczne o różnej ściśliwości.
func sample(kind, n int) []byte {
	b := make([]byte, n)
	x := uint32(kind*2654435761 + 12345)
	for i := range b {
		x = x*1664525 + 1013904223
		switch kind % 5 {
		case 0: // zera
		case 1: // szum
			b[i] = byte(x >> 24)
		case 2: // tekst o małym alfabecie
			b[i] = "abcdefgh \n<>/=\"xml"[x>>24%18]
		case 3: // długie powtórzenia z rzadkimi zmianami
			b[i] = byte(i / 1000)
			if x>>20 == 0 {
				b[i] = byte(x)
			}
		default: // okres dłuższy niż okno
			b[i] = byte((i % 40000) * 7 >> 3)
		}
	}
	return b
}

func inflateAll(t testing.TB, compressed []byte, src io.Reader, chunk int) ([]byte, int64, error) {
	r := NewReader(src)
	var out []byte
	buf := make([]byte, chunk)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if err == io.EOF {
				return out, r.Consumed(), nil
			}
			return out, r.Consumed(), err
		}
		if len(out) > 64<<20 {
			t.Fatal("runaway output")
		}
	}
}

// TestAgainstOracle: dla danych skompresowanych przez compress/flate na każdym poziomie wynik jest równy danym
// źródłowym, niezależnie od tego, jak dzielone jest wejście i wyjście, a liczba zużytych bajtów to długość
// strumienia.
// REQ: INF-001 INF-009 INF-006
func TestAgainstOracle(t *testing.T) {
	for kind := 0; kind < 10; kind++ {
		for _, size := range []int{0, 1, 2, 100, 32767, 32768, 32769, 70000, 300000} {
			data := sample(kind, size)
			for level := flate.HuffmanOnly; level <= flate.BestCompression; level++ {
				var z bytes.Buffer
				w, err := flate.NewWriter(&z, level)
				if err != nil {
					t.Fatal(err)
				}
				// Kilka Flush daje wiele bloków, także pustych bloków niezakodowanych.
				w.Write(data[:len(data)/3])
				w.Flush()
				w.Write(data[len(data)/3:])
				w.Close()
				comp := z.Bytes()
				for _, chunk := range []int{1, 7, 4096, 1 << 20} {
					got, used, err := inflateAll(t, comp, bytes.NewReader(comp), chunk)
					if err != nil || !bytes.Equal(got, data) || used != int64(len(comp)) {
						t.Fatalf("kind %d size %d level %d chunk %d: err %v, %d bytes (want %d), consumed %d of %d",
							kind, size, level, chunk, err, len(got), len(data), used, len(comp))
					}
				}
				got, _, err := inflateAll(t, comp, iotest.OneByteReader(bytes.NewReader(comp)), 333)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("kind %d size %d level %d, one-byte source: err %v", kind, size, level, err)
				}
				// Bajty po końcu strumienia nie są jego częścią: Consumed ich nie liczy.
				tail := append(append([]byte(nil), comp...), 1, 2, 3)
				got, used, err := inflateAll(t, tail, bytes.NewReader(tail), 4096)
				if err != nil || !bytes.Equal(got, data) || used != int64(len(comp)) {
					t.Fatalf("kind %d size %d level %d, trailing bytes: err %v, consumed %d of %d", kind, size, level, err, used, len(comp))
				}
			}
		}
	}
}

func wantKind(t *testing.T, label string, stream []byte, kind Kind, wantOut string) {
	t.Helper()
	for _, chunk := range []int{1, 4096} {
		out, _, err := inflateAll(t, stream, bytes.NewReader(stream), chunk)
		var e *Error
		if !errors.As(err, &e) || e.Kind != kind {
			t.Errorf("%s: error %v, want kind %d", label, err, kind)
		}
		if string(out) != wantOut {
			t.Errorf("%s: output %q before the error, want %q", label, out, wantOut)
		}
		if e != nil && e.Error() == "" {
			t.Errorf("%s: empty message", label)
		}
	}
}

// TestBlockHeader: zarezerwowany typ bloku i koniec wejścia w nagłówku.
// REQ: INF-002 INF-006
func TestBlockHeader(t *testing.T) {
	wantKind(t, "BTYPE 11", new(bitWriter).bits(1, 1).bits(3, 2).out, BlockType, "")
	wantKind(t, "empty input", nil, Truncated, "")
	// Blok nieostatni, po którym nic nie ma.
	wantKind(t, "no final block", new(bitWriter).bits(0, 1).bits(1, 2).lit('a').lit(256).out[:2], Truncated, "a")
	wantKind(t, "reserved type after a block", new(bitWriter).bits(0, 1).bits(1, 2).lit('a').lit(256).bits(1, 1).bits(3, 2).out, BlockType, "a")
}

// TestStoredBlock: LEN i NLEN, pomijanie bitów do granicy bajtu, ucięte dane.
// REQ: INF-003 INF-006
func TestStoredBlock(t *testing.T) {
	block := func(final uint32, l, nl uint32, data string) *bitWriter {
		w := new(bitWriter).bits(final, 1).bits(0, 2).bits(0x15, 5) // bity dopełniające niezerowe: pomijane
		w.bits(l, 16).bits(nl, 16)
		w.out = append(w.out, data...)
		w.n = uint(len(w.out)) * 8
		return w
	}
	out, used, err := inflateAll(t, nil, bytes.NewReader(block(1, 3, 0xFFFC, "abc").out), 10)
	if err != nil || string(out) != "abc" || used != 8 {
		t.Errorf("stored block: %q, consumed %d, %v", out, used, err)
	}
	out, _, err = inflateAll(t, nil, bytes.NewReader(block(1, 0, 0xFFFF, "").out), 10)
	if err != nil || len(out) != 0 {
		t.Errorf("empty stored block: %q, %v", out, err)
	}
	wantKind(t, "NLEN", block(1, 3, 0xFFFD, "abc").out, StoredLength, "")
	wantKind(t, "NLEN equal to LEN", block(1, 3, 3, "abc").out, StoredLength, "")
	wantKind(t, "data cut", block(1, 5, 0xFFFA, "abc").out, Truncated, "abc")
	wantKind(t, "length cut", block(1, 3, 0xFFFC, "").out[:3], Truncated, "")
	// Blok niezakodowany po bloku z kodami, zaczynający się w środku bajtu.
	w := new(bitWriter).bits(0, 1).bits(1, 2).lit('x').lit(256).bits(1, 1).bits(0, 2)
	for w.n%8 != 0 {
		w.bits(1, 1)
	}
	w.bits(2, 16).bits(0xFFFD, 16).bits('y', 8).bits('z', 8)
	out, used, err = inflateAll(t, nil, bytes.NewReader(w.out), 10)
	if err != nil || string(out) != "xyz" || used != int64(len(w.out)) {
		t.Errorf("stored after fixed: %q, consumed %d of %d, %v", out, used, len(w.out), err)
	}
}

// TestLengthsAndDistances: symbole spoza alfabetu, odległość przed początek wyjścia, odwołanie zachodzące na
// siebie i przez granicę bloku.
// REQ: INF-005 INF-001
func TestLengthsAndDistances(t *testing.T) {
	fixed := func() *bitWriter { return new(bitWriter).bits(1, 1).bits(1, 2) }
	// <długość 3, odległość 1> to symbol 257 i kod odległości 0.
	wantKind(t, "distance before the start", fixed().lit(257).code(0, 5).lit(256).out, Distance, "")
	wantKind(t, "distance one too far", fixed().lit('a').lit(257).code(1, 5).lit(256).out, Distance, "a")
	wantKind(t, "length symbol 286", fixed().lit('a').lit(286).code(0, 5).lit(256).out, Distance, "a")
	wantKind(t, "length symbol 287", fixed().lit('a').lit(287).code(0, 5).lit(256).out, Distance, "a")
	wantKind(t, "distance symbol 30", fixed().lit('a').lit(257).code(30, 5).lit(256).out, Distance, "a")
	wantKind(t, "distance symbol 31", fixed().lit('a').lit(257).code(31, 5).lit(256).out, Distance, "a")
	// Po symbolu długości zostaje w ostatnim bajcie 6 bitów zerowych: to kod odległości 0, więc odwołanie się
	// wykonuje, a strumień kończy się dopiero potem.
	wantKind(t, "cut after a distance", fixed().lit('a').lit(257).out, Truncated, "aaaa")
	wantKind(t, "cut in a distance", fixed().lit('a').lit(257).lit('a').out[:2], Truncated, "a")
	// X, Y i <długość 5, odległość 2> dają X,Y,X,Y,X,Y,X (przykład z RFC 1951, 3.2.3).
	out, _, err := inflateAll(t, nil, bytes.NewReader(fixed().lit('X').lit('Y').lit(259).code(1, 5).lit(256).out), 3)
	if err != nil || string(out) != "XYXYXYX" {
		t.Errorf("overlapping copy: %q, %v", out, err)
	}
	// Odwołanie do poprzedniego bloku; długość 258 (symbol 285) i długość z bitami dodatkowymi (symbol 265: 11–12).
	w := new(bitWriter).bits(0, 1).bits(1, 2).lit('a').lit('b').lit(256)
	w.bits(1, 1).bits(1, 2).lit(285).code(1, 5).lit(265).bits(1, 1).code(0, 5).lit(256)
	out, _, err = inflateAll(t, nil, bytes.NewReader(w.out), 1000)
	want := "ab" + string(bytes.Repeat([]byte("ab"), 129)) + string(bytes.Repeat([]byte("b"), 12))
	if err != nil || string(out) != want {
		t.Errorf("copy across blocks: %d bytes, %v", len(out), err)
	}
}

// dynamicHeader zapisuje nagłówek bloku dynamicznego, w którym kod długości kodów daje każdemu z symboli
// 0–15 i 16–18 kod 5-bitowy... tu prościej: wszystkie 19 symboli mają długość 5 poza trzema o długości 4, co
// daje kod zupełny (13×2^-5 + ... ) — zamiast tego używamy 16 symboli po 4 bity (0–15), bez 16–18.
func dynamicHeader(hlit, hdist int, lengths []int) *bitWriter {
	w := new(bitWriter).bits(1, 1).bits(2, 2).bits(uint32(hlit-257), 5).bits(uint32(hdist-1), 5).bits(19-4, 4)
	for _, s := range codeLengthOrder {
		if s < 16 {
			w.bits(4, 3)
		} else {
			w.bits(0, 3)
		}
	}
	// Symbol s ma kod 4-bitowy o wartości s.
	for _, l := range lengths {
		w.code(uint32(l), 4)
	}
	return w
}

// TestDynamicCodes: opis kodów dynamicznych: zakresy, zupełność, powtórzenia, kod końca bloku.
// REQ: INF-004
func TestDynamicCodes(t *testing.T) {
	// Alfabet: 'a' (97) i 256 po jednym bicie; odległości: brak.
	lens := func(hlit, hdist int, set map[int]int) []int {
		l := make([]int, hlit+hdist)
		for k, v := range set {
			l[k] = v
		}
		return l
	}
	ok := dynamicHeader(257, 1, lens(257, 1, map[int]int{'a': 1, 256: 1})).code(0, 1).code(0, 1).code(1, 1)
	out, _, err := inflateAll(t, nil, bytes.NewReader(ok.out), 10)
	if err != nil || string(out) != "aa" {
		t.Errorf("two-symbol code: %q, %v", out, err)
	}
	// Jeden kod odległości o długości 1 (dozwolony wyjątek): 'a', 256 i 257 w kodzie 1,2,2.
	one := dynamicHeader(258, 1, lens(258, 1, map[int]int{'a': 1, 256: 2, 257: 2, 258: 1})).code(0, 1).code(3, 2).code(0, 1).code(2, 2)
	out, _, err = inflateAll(t, nil, bytes.NewReader(one.out), 10)
	if err != nil || string(out) != "aaaa" {
		t.Errorf("single distance code: %q, %v", out, err)
	}
	// Ten sam blok, ale odwołanie używa nieprzypisanego kodu odległości (bit 1).
	wantKind(t, "unused distance code", dynamicHeader(258, 1, lens(258, 1, map[int]int{'a': 1, 256: 2, 257: 2, 258: 1})).code(0, 1).code(3, 2).code(1, 1).bits(0, 32).out, Distance, "a")
	// Brak kodów odległości, a w danych odwołanie.
	wantKind(t, "no distance codes", dynamicHeader(258, 1, lens(258, 1, map[int]int{'a': 1, 256: 2, 257: 2})).code(0, 1).code(3, 2).out, Distance, "a")

	bad := map[string][]byte{
		"no end of block code":           dynamicHeader(257, 1, lens(257, 1, map[int]int{'a': 1, 'b': 1})).out,
		"incomplete literal code":        dynamicHeader(257, 1, lens(257, 1, map[int]int{'a': 2, 256: 1})).out,
		"single literal code":            dynamicHeader(257, 1, lens(257, 1, map[int]int{256: 1})).out,
		"oversubscribed literal code":    dynamicHeader(257, 1, lens(257, 1, map[int]int{'a': 1, 'b': 1, 256: 1})).out,
		"incomplete distance code":       dynamicHeader(257, 2, lens(257, 2, map[int]int{'a': 1, 256: 1, 257: 2, 258: 2})).out,
		"single distance code of 2 bits": dynamicHeader(257, 1, lens(257, 1, map[int]int{'a': 1, 256: 1, 257: 2})).out,
		"oversubscribed distance code":   dynamicHeader(257, 3, lens(257, 3, map[int]int{'a': 1, 256: 1, 257: 1, 258: 1, 259: 1})).out,
		"HLIT 287":                       new(bitWriter).bits(1, 1).bits(2, 2).bits(30, 5).bits(0, 5).bits(15, 4).out,
		"HLIT 288":                       new(bitWriter).bits(1, 1).bits(2, 2).bits(31, 5).bits(0, 5).bits(15, 4).out,
		"HDIST 31":                       dynamicHeader(257, 31, lens(257, 31, map[int]int{'a': 1, 256: 1, 257: 1, 287: 1})).out,
		"HDIST 32":                       dynamicHeader(257, 32, lens(257, 32, map[int]int{'a': 1, 256: 1, 257: 1, 288: 1})).out,
	}
	for label, stream := range bad {
		// Dopełnienie, żeby błąd nie był końcem wejścia.
		wantKind(t, label, append(stream, make([]byte, 64)...), CodeLengths, "")
	}
	// Kod długości kodów: niezupełny i nadmiarowy.
	w := new(bitWriter).bits(1, 1).bits(2, 2).bits(0, 5).bits(0, 5).bits(0, 4).bits(1, 3).bits(0, 3).bits(0, 3).bits(0, 3)
	wantKind(t, "incomplete code length code", append(w.out, make([]byte, 64)...), CodeLengths, "")
	w = new(bitWriter).bits(1, 1).bits(2, 2).bits(0, 5).bits(0, 5).bits(0, 4).bits(1, 3).bits(1, 3).bits(1, 3).bits(0, 3)
	wantKind(t, "oversubscribed code length code", append(w.out, make([]byte, 64)...), CodeLengths, "")

	// Powtórzenia: kod długości kodów z symbolami 16, 17, 18, 0, 1 (16: 2 bity, 17: 2, 18: 2, 0: 3, 1: 3).
	rep := func() *bitWriter {
		w := new(bitWriter).bits(1, 1).bits(2, 2).bits(0, 5).bits(0, 5).bits(18-4, 4)
		set := map[uint8]uint32{16: 2, 17: 2, 18: 2, 0: 3, 1: 3}
		for _, s := range codeLengthOrder[:18] {
			w.bits(set[s], 3)
		}
		return w
	}
	// Kody kanoniczne: 16→00, 17→01, 18→10, 0→110, 1→111.
	wantKind(t, "repeat with no previous length", append(rep().code(0, 2).bits(0, 2).out, make([]byte, 64)...), CodeLengths, "")
	// 18 z licznikiem 138, potem 18 z 138: 276 > 258.
	wantKind(t, "repeat past the end", append(rep().code(2, 2).bits(127, 7).code(2, 2).bits(127, 7).out, make([]byte, 64)...), CodeLengths, "")
	// 97 zer (18: 11+86), 'a'=1, 158 zer (18: 138, 18: 20), 256=1, odległość 0 → „a”.
	good := rep().code(2, 2).bits(86, 7).code(7, 3).code(2, 2).bits(127, 7).code(2, 2).bits(9, 7).code(7, 3).code(6, 3).code(0, 1).code(1, 1)
	out, _, err = inflateAll(t, nil, bytes.NewReader(good.out), 10)
	if err != nil || string(out) != "a" {
		t.Errorf("zero repeats: %q, %v", out, err)
	}
	// 16 powtarza poprzednią długość: 97 zer, 'a'=1, potem 16 (3 razy „1”) dałoby cztery kody 1-bitowe: nadmiar.
	wantKind(t, "repeat makes the code oversubscribed", append(rep().code(2, 2).bits(86, 7).code(7, 3).code(0, 2).bits(0, 2).code(2, 2).bits(127, 7).code(2, 2).bits(6, 7).code(7, 3).code(6, 3).out, make([]byte, 64)...), CodeLengths, "")
	wantKind(t, "cut in the code lengths", rep().code(2, 2).out, Truncated, "")
}

// TestTruncated: każdy właściwy początek poprawnego strumienia kończy się błędem Truncated, a wydane bajty są
// początkiem danych.
// REQ: INF-006 INF-008
func TestTruncated(t *testing.T) {
	data := sample(2, 20000)
	for _, level := range []int{flate.HuffmanOnly, 0, 1, 9} {
		var z bytes.Buffer
		w, _ := flate.NewWriter(&z, level)
		w.Write(data)
		w.Close()
		comp := z.Bytes()
		for cut := 0; cut < len(comp); cut += 1 + cut/50 {
			out, _, err := inflateAll(t, nil, bytes.NewReader(comp[:cut]), 512)
			var e *Error
			if !errors.As(err, &e) || e.Kind != Truncated {
				t.Fatalf("level %d cut %d: error %v", level, cut, err)
			}
			if !bytes.HasPrefix(data, out) {
				t.Fatalf("level %d cut %d: output is not a prefix of the data", level, cut)
			}
		}
	}
}

// TestSourceErrors: błąd źródła jest zwracany bez zmiany; źródło, które nic nie daje, nie zawiesza dekodera.
// REQ: INF-008
func TestSourceErrors(t *testing.T) {
	boom := errors.New("boom")
	var z bytes.Buffer
	w, _ := flate.NewWriter(&z, 6)
	w.Write(sample(1, 50000))
	w.Close()
	src := io.MultiReader(bytes.NewReader(z.Bytes()[:1000]), iotest.ErrReader(boom))
	if _, _, err := inflateAll(t, nil, src, 100); err != boom {
		t.Errorf("source error: %v", err)
	}
	if _, _, err := inflateAll(t, nil, stuck{}, 100); err != io.ErrNoProgress {
		t.Errorf("stuck source: %v", err)
	}
	if n, err := NewReader(bytes.NewReader(nil)).Read(nil); n != 0 || err != nil {
		t.Errorf("empty read: %d, %v", n, err)
	}
}

type stuck struct{}

func (stuck) Read([]byte) (int, error) { return 0, nil }

// TestNoAllocations: dekodowanie nie alokuje poza samym dekoderem.
// REQ: INF-007
func TestNoAllocations(t *testing.T) {
	var z bytes.Buffer
	w, _ := flate.NewWriter(&z, 6)
	w.Write(sample(2, 200000))
	w.Close()
	src := bytes.NewReader(z.Bytes())
	r := NewReader(src)
	buf := make([]byte, 8192)
	allocs := testing.AllocsPerRun(5, func() {
		src.Reset(z.Bytes())
		*r = Reader{src: src}
		for {
			if _, err := r.Read(buf); err != nil {
				break
			}
		}
	})
	if allocs != 0 {
		t.Errorf("%v allocations per stream", allocs)
	}
}

// FuzzInflate: dla dowolnego wejścia dekoder nie panikuje i kończy pracę; gdy on i compress/flate oba przyjmują
// strumień, dają te same bajty; strumień przyjęty przez nas musi przyjąć też compress/flate; szybka ścieżka
// daje to samo co zwykła: bajty, rodzaj i miejsce błędu, liczbę zużytych bajtów (INF-010).
func FuzzInflate(f *testing.F) {
	for kind := 0; kind < 5; kind++ {
		for _, level := range []int{flate.HuffmanOnly, 0, 1, 6} {
			var z bytes.Buffer
			w, _ := flate.NewWriter(&z, level)
			w.Write(sample(kind, 3000))
			w.Close()
			f.Add(z.Bytes())
		}
	}
	f.Fuzz(func(t *testing.T, stream []byte) {
		const limit = 8 << 20
		if fast, plain := decodeWith(stream, false, 4096, 32<<10, limit), decodeWith(stream, true, 4096, 32<<10, limit); !fast.equal(plain) {
			t.Fatalf("the fast path gives %d bytes, kind %d at %d, consumed %d; the plain path %d bytes, kind %d at %d, consumed %d",
				len(fast.out), fast.kind, fast.offset, fast.consumed, len(plain.out), plain.kind, plain.offset, plain.consumed)
		}
		if pieces := decodeWith(stream, false, 11, 700, limit); !pieces.equal(decodeWith(stream, true, 11, 700, limit)) {
			t.Fatalf("the fast path differs from the plain path with input in pieces of 11 bytes")
		}
		ours, oursErr := io.ReadAll(io.LimitReader(NewReader(bytes.NewReader(stream)), limit))
		fr := flate.NewReader(bytes.NewReader(stream))
		theirs, theirsErr := io.ReadAll(io.LimitReader(fr, limit))
		if oursErr != nil {
			var e *Error
			if !errors.As(oursErr, &e) {
				t.Fatalf("error %v is not an *Error", oursErr)
			}
			// Bajty wydane przed błędem są początkiem tego, co daje wyrocznia, o ile ona doszła tak daleko.
			if len(theirs) >= len(ours) && !bytes.Equal(ours, theirs[:len(ours)]) {
				t.Fatalf("output before the error differs from compress/flate")
			}
			return
		}
		if theirsErr != nil {
			t.Fatalf("accepted a stream compress/flate rejects: %v", theirsErr)
		}
		if !bytes.Equal(ours, theirs) {
			t.Fatalf("output differs from compress/flate")
		}
	})
}

func BenchmarkInflate(b *testing.B) {
	data := sample(2, 4<<20)
	var z bytes.Buffer
	w, _ := flate.NewWriter(&z, 6)
	w.Write(data)
	w.Close()
	buf := make([]byte, 32<<10)
	b.Run("ours", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for i := 0; i < b.N; i++ {
			r := NewReader(bytes.NewReader(z.Bytes()))
			for {
				if _, err := r.Read(buf); err != nil {
					break
				}
			}
		}
	})
	b.Run("compress-flate", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for i := 0; i < b.N; i++ {
			r := flate.NewReader(bytes.NewReader(z.Bytes()))
			for {
				if _, err := r.Read(buf); err != nil {
					break
				}
			}
		}
	})
}
