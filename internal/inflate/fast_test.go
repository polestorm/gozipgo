package inflate

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// pieces podaje dane w kawałkach po n bajtów.
type pieces struct {
	data []byte
	n    int
}

func (c *pieces) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	k := min(c.n, len(p), len(c.data))
	copy(p, c.data[:k])
	c.data = c.data[k:]
	return k, nil
}

// outcome to wszystko, co wywołujący widzi po zdekodowaniu strumienia.
type outcome struct {
	out      []byte
	kind     Kind   // rodzaj wady, 0 gdy strumień jest poprawny
	offset   int64  // miejsce wady
	other    string // błąd, który nie jest *Error
	consumed int64
}

func (o outcome) equal(p outcome) bool {
	return bytes.Equal(o.out, p.out) && o.kind == p.kind && o.offset == p.offset && o.other == p.other &&
		o.consumed == p.consumed
}

// decodeWith dekoduje strumień zwykłą ścieżką (slow) albo z szybką, z wejściem w kawałkach po chunk bajtów
// i wyjściem po buf bajtów, najwyżej limit bajtów wyjścia.
func decodeWith(stream []byte, slow bool, chunk, buf, limit int) outcome {
	r := NewReader(&pieces{stream, chunk})
	r.slow = slow
	var o outcome
	p := make([]byte, buf)
	for len(o.out) < limit {
		n, err := r.Read(p)
		o.out = append(o.out, p[:n]...)
		if err != nil {
			var e *Error
			if errors.As(err, &e) {
				o.kind, o.offset = e.Kind, e.Offset
			} else if err != io.EOF {
				o.other = err.Error()
			}
			break
		}
	}
	o.consumed = r.Consumed()
	return o
}

// samePaths sprawdza, że szybka ścieżka daje to samo co zwykła przy różnych podziałach wejścia i wyjścia. Kawałki
// wejścia po 8 i 9 bajtów stoją na progu, od którego szybka ścieżka w ogóle rusza; 4095 i 4096 na granicy bufora.
func samePaths(t *testing.T, name string, stream []byte) outcome {
	t.Helper()
	return samePathsFor(t, name, stream, []int{1 << 20, 4096, 4095, 9, 8, 1}, []int{32 << 10, 1000, 1})
}

func samePathsFor(t *testing.T, name string, stream []byte, chunks, bufs []int) outcome {
	t.Helper()
	want := decodeWith(stream, true, 1<<20, 32<<10, 1<<30)
	for _, chunk := range chunks {
		for _, buf := range bufs {
			if chunk == 1 && buf == 1 && len(stream) > 1<<16 {
				continue
			}
			if got := decodeWith(stream, false, chunk, buf, 1<<30); !got.equal(want) {
				t.Fatalf("%s, input in pieces of %d, output buffer %d: the fast path gives %d bytes, kind %d at %d, consumed %d; the plain path %d bytes, kind %d at %d, consumed %d",
					name, chunk, buf, len(got.out), got.kind, got.offset, got.consumed, len(want.out), want.kind, want.offset, want.consumed)
			}
		}
	}
	return want
}

// fastInputs to dane, których strumienie przechodzą przez różne części szybkiej ścieżki: krótkie kody (text),
// kody dłuższe niż tablica (skew: kilka częstych symboli i 250 rzadkich), same znaki (noise), odwołania zachodzące
// na siebie (zeros) i dalekie, sięgające w poprzedni obrót okna (far).
func fastInputs() map[string][]byte {
	m := map[string][]byte{}
	x := uint32(7)
	next := func() uint32 { x = x*1664525 + 1013904223; return x >> 8 }
	words := []string{"<w:p>", "</w:p>", "<w:r>", "<w:t>", "alpha", "beta", "gamma", " ", "\n", "0123456789", "zażółć"}
	var text []byte
	for len(text) < 600000 {
		text = append(text, words[next()%uint32(len(words))]...)
	}
	m["text"] = text
	noise := make([]byte, 200000)
	for i := range noise {
		noise[i] = byte(next())
	}
	m["noise"] = noise
	m["zeros"] = make([]byte, 400000)
	m["far"] = append(bytes.Repeat(noise[:32768], 3), bytes.Repeat(noise[:32700], 3)...)
	skew := make([]byte, 300000)
	for i := range skew {
		switch v := next() % 1000; {
		case v < 600:
			skew[i] = 'a'
		case v < 900:
			skew[i] = byte('b' + v%4)
		default:
			skew[i] = byte(next())
		}
	}
	m["skew"] = skew
	return m
}

// TestFastPathEquivalent: szybka ścieżka dekodera daje te same bajty, ten sam błąd w tym samym miejscu i tę samą
// liczbę zużytych bajtów co zwykła: dla strumieni poprawnych, uciętych, z odwróconym bitem i z podmienionymi
// bajtami, oraz dla ziaren i znalezisk fuzzera.
// REQ: INF-010
func TestFastPathEquivalent(t *testing.T) {
	x := uint32(99)
	next := func() int { x = x*1664525 + 1013904223; return int(x >> 8) }
	inputs := fastInputs()
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names) // te same uszkodzenia w każdym przebiegu
	for _, name := range names {
		data := inputs[name]
		for _, level := range []int{1, 6, 9, flate.HuffmanOnly} {
			var b bytes.Buffer
			w, _ := flate.NewWriter(&b, level)
			w.Write(data)
			w.Close()
			stream := b.Bytes()
			if got := samePaths(t, name, stream); !bytes.Equal(got.out, data) || got.kind != 0 || got.consumed != int64(len(stream)) {
				t.Fatalf("%s, level %d: the stream does not decode to its data", name, level)
			}
			for i := 0; i < 60; i++ {
				bad := append([]byte(nil), stream...)
				switch i % 3 {
				case 0:
					bad[next()%len(bad)] ^= 1 << (next() % 8)
				case 1:
					bad = bad[:next()%len(bad)]
				default:
					at := next() % len(bad)
					for k := at; k < min(at+1+next()%6, len(bad)); k++ {
						bad[k] = byte(next())
					}
				}
				// Uszkodzeń jest dużo, więc każde przy dwóch podziałach wejścia i jednym wyjścia.
				samePathsFor(t, name, bad, []int{1 << 20, 9 + i%5}, []int{1000 + i})
			}
		}
	}
	files, _ := filepath.Glob("testdata/fuzz/*/*")
	for _, f := range files {
		if raw, err := os.ReadFile(f); err == nil {
			samePaths(t, f, raw)
		}
	}
}

// tokens składa blok ze stałymi kodami (RFC 1951, 3.2.6) z kolejnych znaków i odwołań i prowadzi obok wynik,
// który ten blok ma dać, liczony wprost z definicji odwołania (3.2.3).
type tokens struct {
	w    bitWriter
	want []byte
}

func (k *tokens) literal(b byte) {
	k.w.lit(int(b))
	k.want = append(k.want, b)
}

func (k *tokens) match(length, dist int) {
	s := len(lengthBase) - 1
	for int(lengthBase[s]) > length {
		s--
	}
	if length == 258 {
		s = 28
	} else if s == 28 {
		s = 27 // 258 ma własny symbol; długości 227–257 należą do symbolu 284
	}
	k.w.lit(257 + s)
	k.w.bits(uint32(length-int(lengthBase[s])), uint(lengthExtra[s]))
	d := len(distBase) - 1
	for int(distBase[d]) > dist {
		d--
	}
	k.w.code(uint32(d), 5)
	k.w.bits(uint32(dist-int(distBase[d])), uint(distExtra[d]))
	for i := 0; i < length; i++ {
		k.want = append(k.want, k.want[len(k.want)-dist])
	}
}

// TestFastPathWindow: odwołania na brzegach okna. Okno dekodera jest kołowe, więc odwołanie o odległości 32 768
// czyta bajty, które leżą tuż za bieżącym końcem wyjścia i należą do poprzedniego obrotu: kopia poprzedniego
// odwołania nie może ich naruszyć. Sprawdzane są też odwołania, których źródło przechodzi przez koniec okna,
// odwołania zachodzące na siebie i wszystkie długości na ostatnich bajtach okna, dla szybkiej i zwykłej ścieżki.
// REQ: INF-010 INF-001
func TestFastPathWindow(t *testing.T) {
	x := uint32(5)
	next := func() int { x = x*1664525 + 1013904223; return int(x >> 8) }
	check := func(name string, k *tokens) {
		t.Helper()
		for i := 0; i < 40; i++ {
			k.literal(byte(next())) // zapas wejścia: szybka ścieżka potrzebuje ośmiu bajtów przed sobą
		}
		k.w.lit(endOfBlock)
		if got := samePaths(t, name, k.w.out); got.kind != 0 || !bytes.Equal(got.out, k.want) {
			t.Fatalf("%s: %d bytes, kind %d; want %d bytes", name, len(got.out), got.kind, len(k.want))
		}
	}
	start := func(literals int) *tokens {
		k := &tokens{}
		k.w.bits(1, 1).bits(1, 2)
		for i := 0; i < literals; i++ {
			k.literal(byte(next()))
		}
		return k
	}

	// Odwołania o największej odległości jedno za drugim, różnych długości, przez trzy obroty okna.
	k := start(windowSize)
	for len(k.want) < 4*windowSize {
		k.match([]int{3, 4, 7, 8, 9, 100, 257, 258}[next()%8], windowSize)
	}
	check("farthest matches in a row", k)

	// To samo z krótkim odwołaniem pomiędzy: po każdej kopii następna czyta bajty tuż za jej końcem.
	k = start(windowSize)
	for len(k.want) < 3*windowSize {
		k.match(3+next()%20, 1+next()%16)
		k.match(3+next()%256, windowSize)
		k.match(3+next()%256, windowSize-next()%8)
	}
	check("near and farthest matches interleaved", k)

	// Każda pozycja końca wyjścia w ostatnich 300 bajtach okna, z odwołaniem przechodzącym przez koniec okna.
	for _, tail := range []int{0, 1, 2, 3, 257, 258, 259, 260, 300} {
		for _, dist := range []int{1, 2, 8, 259, 30000, windowSize - 1, windowSize} {
			k = start(windowSize)
			for len(k.want) < 2*windowSize-tail-258 {
				k.match(258, dist)
			}
			for len(k.want) < 2*windowSize-tail {
				k.literal(byte(next()))
			}
			k.match(258, dist)
			k.match(258, windowSize-100)
			k.match(5, 3)
			check("matches across the end of the window", k)
		}
	}

	// Mieszanka: znaki i odwołania o odległościach i długościach z brzegów zakresów.
	k = start(1)
	for len(k.want) < 5*windowSize {
		if next()%3 == 0 {
			k.literal(byte(next()))
			continue
		}
		dist := []int{1, 2, 3, 7, 8, 9, 257, 258, 259, 1 + next()%windowSize, windowSize - 1, windowSize}[next()%12]
		length := []int{3, 4, 8, 9, 16, 226, 227, 257, 258, 3 + next()%256}[next()%10]
		if dist > len(k.want) {
			dist = len(k.want)
		}
		k.match(length, dist)
	}
	check("mixed literals and matches", k)

	// Odległość większa o jeden od liczby wydanych bajtów jest błędem, równa jej nie jest.
	for _, before := range []int{1, 2, 100, 5000} {
		for _, over := range []int{0, 1} {
			k = start(before)
			want := append([]byte(nil), k.want...)
			k.match(3, before)
			if over == 1 {
				k = start(0)
				for _, b := range want {
					k.literal(b)
				}
				k.w.lit(257)
				d := len(distBase) - 1
				for int(distBase[d]) > before+1 {
					d--
				}
				k.w.code(uint32(d), 5).bits(uint32(before+1-int(distBase[d])), uint(distExtra[d]))
			} else {
				want = k.want
			}
			for i := 0; i < 40; i++ {
				k.w.lit(0)
			}
			got := samePaths(t, "distance at the start of the output", k.w.out)
			if over == 1 && (got.kind != Distance || !bytes.Equal(got.out, want)) {
				t.Fatalf("distance %d after %d bytes: kind %d after %d bytes, want an error", before+1, before, got.kind, len(got.out))
			}
			if over == 0 && (got.kind != Truncated || !bytes.Equal(got.out, append(want, make([]byte, 40)...))) {
				t.Fatalf("distance %d after %d bytes: kind %d after %d bytes, want the match and the literals", before, before, got.kind, len(got.out))
			}
		}
	}

	// Odwołanie sięgające przed początek wyjścia: ten sam błąd w tym samym miejscu na obu ścieżkach, po tych
	// samych bajtach wyjścia.
	for _, before := range []int{1, 100, 30000} {
		k = start(before)
		for i := 0; i < 40; i++ {
			k.w.lit(0) // zapas wejścia, żeby szybka ścieżka doszła do błędnego odwołania
		}
		want := append(k.want, make([]byte, 40)...)
		k.w.lit(257).code(uint32(29), 5).bits(8191, 13)
		for i := 0; i < 40; i++ {
			k.w.lit(0)
		}
		if got := samePaths(t, "distance before the start", k.w.out); got.kind != Distance || !bytes.Equal(got.out, want) {
			t.Fatalf("distance before the start after %d bytes: kind %d after %d bytes", before, got.kind, len(got.out))
		}
	}
}

// canonical zwraca kody Huffmana dla podanych długości (algorytm z RFC 1951, 3.2.2).
func canonical(lengths []int) []uint32 {
	var count, next [maxCodeBits + 2]uint32
	for _, l := range lengths {
		count[l]++
	}
	count[0] = 0
	code := uint32(0)
	for l := 1; l <= maxCodeBits; l++ {
		code = (code + count[l-1]) << 1
		next[l] = code
	}
	codes := make([]uint32, len(lengths))
	for s, l := range lengths {
		if l != 0 {
			codes[s] = next[l]
			next[l]++
		}
	}
	return codes
}

// TestFastPathLongestStep: jeden krok szybkiej ścieżki zużywa najwyżej 48 bitów (kod długości 15 i 5 bitów
// dodatkowych, kod odległości 15 i 13 bitów dodatkowych) i tyle musi mieć w buforze, jakikolwiek był stan bufora
// przed krokiem. Blok ma kody wszystkich długości od 1 do 15, więc sprawdzane są też kody dłuższe niż tablica.
// REQ: INF-010
func TestFastPathLongestStep(t *testing.T) {
	lit, dist := make([]int, 286), make([]int, 30)
	for i := 0; i < 14; i++ {
		lit[i], dist[i] = i+1, i+1
	}
	lit[endOfBlock], lit[284], dist[28], dist[29] = 15, 15, 15, 15
	litCode, distCode := canonical(lit), canonical(dist)
	w := dynamicHeader(286, 30, append(append([]int{}, lit...), dist...))
	var want []byte
	literal := func(b int) {
		w.code(litCode[b], uint(lit[b]))
		want = append(want, byte(b))
	}
	match := func(length, back int) {
		w.code(litCode[284], 15).bits(uint32(length-227), 5)
		d := 13
		switch {
		case back >= 24577:
			d = 29
		case back >= 16385:
			d = 28
		}
		w.code(distCode[d], uint(dist[d])).bits(uint32(back-int(distBase[d])), uint(distExtra[d]))
		for i := 0; i < length; i++ {
			want = append(want, want[len(want)-back])
		}
	}
	x := uint32(3)
	next := func() int { x = x*1664525 + 1013904223; return int(x >> 8) }
	for len(want) < windowSize {
		literal(next() % 14)
	}
	// Przed każdym najdłuższym krokiem znaki o kodach każdej długości, żeby bufor bitów był w każdym stanie.
	for round := 0; round < 400; round++ {
		for n := next() % 4; n >= 0; n-- {
			literal(next() % 14)
		}
		switch next() % 3 {
		case 0:
			match(227+next()%31, 24577+next()%8192)
		case 1:
			match(227+next()%31, 16385+next()%8192)
		default:
			match(227+next()%31, 97+next()%32)
		}
	}
	for i := 0; i < 40; i++ {
		literal(next() % 14)
	}
	w.code(litCode[endOfBlock], 15)
	if got := samePaths(t, "longest step", w.out); got.kind != 0 || !bytes.Equal(got.out, want) {
		t.Fatalf("%d bytes, kind %d; want %d bytes", len(got.out), got.kind, len(want))
	}
}

// TestFastPathRefusals: symbole, których szybka ścieżka nie bierze, dają na obu ścieżkach ten sam błąd w tym
// samym miejscu, po tych samych bajtach wyjścia: symbol długości 286, symbol odległości 30 i odwołanie w bloku
// bez kodów odległości. Po błędnym symbolu stoi dość wejścia, żeby trafiła na niego szybka ścieżka.
// REQ: INF-010 INF-005
func TestFastPathRefusals(t *testing.T) {
	fixed := func(bad func(w *bitWriter)) ([]byte, []byte) {
		w := new(bitWriter).bits(1, 1).bits(1, 2)
		var want []byte
		for i := 0; i < 50; i++ {
			w.lit('a' + i%20)
			want = append(want, byte('a'+i%20))
		}
		bad(w)
		for i := 0; i < 50; i++ {
			w.lit('z')
		}
		return w.out, want
	}
	cases := map[string]func(w *bitWriter){
		"length symbol 286":  func(w *bitWriter) { w.lit(286) },
		"distance symbol 30": func(w *bitWriter) { w.lit(257).code(30, 5) },
	}
	for name, bad := range cases {
		stream, want := fixed(bad)
		if got := samePaths(t, name, stream); got.kind != Distance || !bytes.Equal(got.out, want) {
			t.Errorf("%s: kind %d after %d bytes, want kind %d after %d", name, got.kind, len(got.out), Distance, len(want))
		}
	}
	// Blok dynamiczny: 'a' ma kod 1-bitowy, koniec bloku i symbol długości 257 po dwa bity; kodów odległości brak.
	lens := make([]int, 258+1)
	lens['a'], lens[256], lens[257] = 1, 2, 2
	w := dynamicHeader(258, 1, lens)
	for i := 0; i < 200; i++ {
		w.code(0, 1)
	}
	w.code(3, 2)
	for i := 0; i < 200; i++ {
		w.code(0, 1)
	}
	if got := samePaths(t, "match without distance codes", w.out); got.kind != Distance || len(got.out) != 200 {
		t.Errorf("match without distance codes: kind %d after %d bytes, want kind %d after 200", got.kind, len(got.out), Distance)
	}
}
