package deflate

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/polestorm/gozipgo/internal/inflate"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden.tsv")

// sample to dane deterministyczne o różnej ściśliwości.
func sample(kind, n int) []byte {
	b := make([]byte, n)
	x := uint32(kind)*2654435761 + 12345
	next := func() uint32 { x = x*1664525 + 1013904223; return x >> 8 }
	switch kind % 6 {
	case 0: // zera
	case 1: // szum
		for i := range b {
			b[i] = byte(next())
		}
	case 2: // znaczniki o małym słowniku, jak części dokumentu biurowego
		words := []string{"<w:p>", "</w:p>", "<w:r>", "</w:r>", "<w:t>", "</w:t>", "<w:rPr>", "<w:b/>", "</w:rPr>",
			"Lorem", "ipsum", "dolor", "sit", "amet", "zażółć", "gęślą", "jaźń", " ", " ", " ", "\n", "0", "1", "2024"}
		for i := 0; i < n; {
			i += copy(b[i:], words[next()%uint32(len(words))])
		}
	case 3: // długie powtórzenia z rzadkimi zmianami
		for i := range b {
			b[i] = byte(i / 1000)
			if next()>>12 == 0 {
				b[i] = byte(next())
			}
		}
	case 4: // okres dłuższy niż okno
		for i := range b {
			b[i] = byte((i % 40000) * 7 >> 3)
		}
	default: // liczby w wierszach, jak arkusz zapisany tekstem
		for i := 0; i < n; {
			i += copy(b[i:], fmt.Sprintf("%d;%d.%02d;%s\n", next()%100000, next()%1000, next()%100, []string{"PLN", "EUR", "USD"}[next()%3]))
		}
	}
	return b
}

var sizes = []int{0, 1, 2, 3, 4, 100, 257, 258, 259, 4096, 32768, 65277, 65278, 65535, 65536, 131072, 131073, 300000, 1 << 20}

// compress koduje data, dzieląc je na kawałki o długościach z chunks (cyklicznie; pusty wycinek: jeden zapis).
func compress(t testing.TB, data []byte, chunks ...int) []byte {
	var out bytes.Buffer
	w := NewWriter(&out)
	writeAll(t, w, data, chunks...)
	return out.Bytes()
}

func writeAll(t testing.TB, w *Writer, data []byte, chunks ...int) {
	for i := 0; len(data) > 0 || i == 0; i++ {
		n := len(data)
		if len(chunks) > 0 {
			n = min(n, chunks[i%len(chunks)])
		}
		if k, err := w.Write(data[:n]); err != nil || k != n {
			t.Fatalf("Write: %d, %v", k, err)
		}
		data = data[n:]
		if n == 0 && len(data) > 0 {
			t.Fatal("zero chunk")
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// expand rozpakowuje własnym dekoderem i sprawdza, że strumień kończy się dokładnie z danymi.
func expand(t testing.TB, compressed []byte) []byte {
	r := inflate.NewReader(bytes.NewReader(compressed))
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("own decoder: %v", err)
	}
	if r.Consumed() != int64(len(compressed)) {
		t.Fatalf("own decoder consumed %d of %d bytes", r.Consumed(), len(compressed))
	}
	return out
}

func expandStdlib(t testing.TB, compressed []byte) []byte {
	src := bytes.NewReader(compressed)
	out, err := io.ReadAll(flate.NewReader(src))
	if err != nil {
		t.Fatalf("compress/flate: %v", err)
	}
	if src.Len() != 0 {
		t.Fatalf("compress/flate left %d bytes", src.Len())
	}
	return out
}

// TestRoundTrip: każdy strumień kodera jest poprawnym strumieniem DEFLATE; własny dekoder i compress/flate
// oddają dane wejściowe i zużywają strumień co do bajta.
// REQ: ZW-006
func TestRoundTrip(t *testing.T) {
	for kind := 0; kind < 6; kind++ {
		for _, n := range sizes {
			data := sample(kind, n)
			c := compress(t, data)
			if got := expand(t, c); !bytes.Equal(got, data) {
				t.Errorf("kind %d, %d bytes: own decoder gives different data", kind, n)
			}
			if got := expandStdlib(t, c); !bytes.Equal(got, data) {
				t.Errorf("kind %d, %d bytes: compress/flate gives different data", kind, n)
			}
		}
	}
}

// TestSplitIndependence: bajty wyjścia nie zależą od podziału danych na wywołania Write ani od tego, co koder
// kodował przed Reset.
// REQ: ZW-008
func TestSplitIndependence(t *testing.T) {
	reused := NewWriter(io.Discard)
	for kind := 0; kind < 6; kind++ {
		for _, n := range []int{0, 5, 1000, 65536, 300000} {
			data := sample(kind, n)
			want := compress(t, data)
			for _, chunks := range [][]int{{1}, {7}, {261, 1, 262}, {4096}, {65535, 3}, {131071, 2}, {131072}, {200000}} {
				if n > 70000 && chunks[0] == 1 && len(chunks) == 1 && testing.Short() {
					continue
				}
				if got := compress(t, data, chunks...); !bytes.Equal(got, want) {
					t.Errorf("kind %d, %d bytes, chunks %v: output differs from the single write", kind, n, chunks)
				}
			}
			var out bytes.Buffer
			reused.Reset(&out)
			writeAll(t, reused, data)
			if !bytes.Equal(out.Bytes(), want) {
				t.Errorf("kind %d, %d bytes: output of a reused writer differs", kind, n)
			}
		}
	}
}

// firstBlock zwraca znacznik ostatniego bloku i typ pierwszego bloku strumienia.
func firstBlock(c []byte) (final bool, btype int) { return c[0]&1 == 1, int(c[0] >> 1 & 3) }

// TestBlockForms: koder wybiera najtańszą postać bloku: dane puste to jeden blok stały z samym końcem bloku,
// szum idzie blokami niezakodowanymi (narzut pięciu bajtów na blok), powtórzenia blokami dynamicznymi.
// REQ: ZW-006
func TestBlockForms(t *testing.T) {
	if c := compress(t, nil); !bytes.Equal(c, []byte{0x03, 0x00}) {
		t.Errorf("empty input: % x, want 03 00", c)
	}
	noise := sample(1, 200000)
	c := compress(t, noise)
	if _, bt := firstBlock(c); bt != 0 {
		t.Errorf("noise: first block type %d, want stored", bt)
	}
	if blocks := (len(noise) + maxTokens - 1) / maxTokens; len(c) > len(noise)+5*blocks {
		t.Errorf("noise: %d bytes from %d, want at most 5 bytes for each of %d blocks", len(c), len(noise), blocks)
	}
	if _, bt := firstBlock(compress(t, sample(2, 200000))); bt != 2 {
		t.Errorf("markup: first block type %d, want dynamic", bt)
	}
	if _, bt := firstBlock(compress(t, []byte("abc"))); bt != 1 {
		t.Errorf("three bytes: first block type %d, want fixed", bt)
	}
	// Po długim odcinku bez dopasowań (koder przeskakuje wtedy pozycje bez szukania) dane ściśliwe są nadal
	// kompresowane: przeskok jest ograniczony, a pierwsze trafienie go zeruje.
	markup := sample(2, 200000)
	mixed := len(compress(t, append(sample(1, 1<<20), markup...)))
	if limit := len(compress(t, sample(1, 1<<20))) + len(compress(t, markup))*105/100; mixed > limit {
		t.Errorf("markup after noise: %d bytes, want at most %d", mixed, limit)
	}
	// Stosunek kompresji nie przekracza 1000:1, więc domyślny limit czytnika (MaxRatio) przyjmuje wynik kodera.
	zeros := sample(0, 8<<20)
	if c := compress(t, zeros); len(zeros)/len(c) >= 1000 || len(zeros)/len(c) < 800 {
		t.Errorf("zeros: ratio %d:1, want between 800:1 and 1000:1", len(zeros)/len(c))
	}
}

// TestCodeLengths: długości z build dają kod pełny (suma Krafta równa 1), nie dłuższy niż limit, o koszcie
// równym kodowi Huffmana, gdy ten mieści się w limicie, i nie mniejszym w przeciwnym razie.
// REQ: ZW-006
func TestCodeLengths(t *testing.T) {
	huffmanCost := func(freq []int32) (cost int64, depth int) {
		type node struct {
			w     int64
			depth int
		}
		var nodes []node
		for _, f := range freq {
			if f > 0 {
				nodes = append(nodes, node{int64(f), 0})
			}
		}
		for len(nodes) > 1 {
			a, b := 0, 1
			if nodes[b].w < nodes[a].w {
				a, b = b, a
			}
			for i := 2; i < len(nodes); i++ {
				if nodes[i].w < nodes[a].w {
					a, b = i, a
				} else if nodes[i].w < nodes[b].w {
					b = i
				}
			}
			merged := node{nodes[a].w + nodes[b].w, max(nodes[a].depth, nodes[b].depth) + 1}
			cost += merged.w
			nodes[a] = merged
			nodes[b] = nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)-1]
		}
		return cost, nodes[0].depth
	}
	x := uint32(7)
	next := func() uint32 { x = x*1664525 + 1013904223; return x >> 8 }
	check := func(name string, freq []int32, limit int) {
		var c code
		c.build(freq, limit)
		kraft, cost, used := 0, int64(0), 0
		for i, f := range freq {
			l := int(c.length[i])
			if f > 0 {
				used++
				if l == 0 {
					t.Fatalf("%s: symbol %d is used and has no code", name, i)
				}
			}
			if l > limit {
				t.Fatalf("%s: length %d above the limit %d", name, l, limit)
			}
			if l > 0 {
				kraft += 1 << (limit - l)
			}
			cost += int64(f) * int64(l)
		}
		if kraft != 1<<limit {
			t.Fatalf("%s: Kraft sum %d/%d", name, kraft, 1<<limit)
		}
		if used < 2 {
			return
		}
		best, depth := huffmanCost(freq)
		if depth <= limit && cost != best || cost < best {
			t.Fatalf("%s: cost %d, Huffman cost %d at depth %d (limit %d)", name, cost, best, depth, limit)
		}
	}
	for round := 0; round < 2000; round++ {
		n := []int{numLitLen, numDist, numCodeLen}[round%3]
		limit := []int{maxCodeBits, maxCodeBits, maxCLBits}[round%3]
		freq := make([]int32, n)
		used := 1 + int(next())%n
		for i := 0; i < used; i++ {
			freq[next()%uint32(n)] = int32(1 + next()%(1<<(next()%16)))
		}
		check(fmt.Sprint("random ", round), freq, limit)
	}
	// Częstości Fibonacciego dają kod Huffmana o największej głębokości: limit musi zadziałać.
	fib := make([]int32, numDist)
	a, b := int32(1), int32(1)
	for i := range fib {
		fib[i] = a
		a, b = b, a+b
	}
	check("fibonacci 30 at 15", fib, maxCodeBits)
	check("fibonacci 19 at 7", fib[:numCodeLen], maxCLBits)
	check("none", make([]int32, numDist), maxCodeBits)
	one := make([]int32, numDist)
	one[0] = 5
	check("only symbol 0", one, maxCodeBits)
	one[0], one[7] = 0, 5
	check("only symbol 7", one, maxCodeBits)
}

// TestSymbols: tabele symboli długości i odległości zgadzają się z tabelami RFC 1951, 3.2.5.
// REQ: ZW-006
func TestSymbols(t *testing.T) {
	lengthBase := []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lengthBits := []int{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	for l := 3; l <= 258; l++ {
		s := int(lengthSymbol[l-3])
		if l < lengthBase[s] || l >= lengthBase[s]+1<<lengthBits[s] || int(lengthExtra[l-3]) != lengthBits[s] || l == 258 && s != 28 {
			t.Errorf("length %d: symbol %d with %d extra bits", l, 257+s, lengthExtra[l-3])
		}
	}
	distBase := []int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	for d := 1; d <= 32768; d++ {
		s, e := distSymbol(d)
		if s >= 30 || d < distBase[s] || d >= distBase[s]+1<<e || s < 29 && d >= distBase[s+1] {
			t.Errorf("distance %d: symbol %d with %d extra bits", d, s, e)
		}
	}
}

// TestGolden: skrót wyjścia kodera dla ustalonych danych jest równy zapisanemu wzorcowi. Wzorzec jest wspólny
// dla wszystkich systemów i wersji Go z CI: zmiana oznacza, że koder daje inne bajty.
// REQ: ZW-005
func TestGolden(t *testing.T) {
	var b strings.Builder
	b.WriteString("kind\tbytes\tcompressed bytes\tsha256 of the compressed stream\n")
	for kind := 0; kind < 6; kind++ {
		for _, n := range []int{0, 1, 1000, 65536, 1 << 20} {
			c := compress(t, sample(kind, n))
			h := sha256.Sum256(c)
			fmt.Fprintf(&b, "%d\t%d\t%d\t%s\n", kind, n, len(c), hex.EncodeToString(h[:]))
		}
	}
	const path = "testdata/golden.tsv"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("the output of the encoder changed; if intended, record the decision and run with -update-golden\n%s", got)
	}
}

// TestSizeAgainstStdlib: wynik na danych znacznikowych i tabelarycznych nie przekracza 1,1 wyniku compress/flate
// na poziomie domyślnym (pomiar do publikacji robi tools/bench).
// REQ: ZW-006
func TestSizeAgainstStdlib(t *testing.T) {
	if testing.Short() {
		t.Skip("compresses 12 MiB twice; the published measurement is checked by TestBenchWriteRecorded")
	}
	for _, kind := range []int{2, 5, 3} {
		data := sample(kind, 4<<20)
		ours := len(compress(t, data))
		var buf bytes.Buffer
		fw, _ := flate.NewWriter(&buf, flate.DefaultCompression)
		fw.Write(data)
		fw.Close()
		ratio := float64(ours) / float64(buf.Len())
		t.Logf("kind %d: %d bytes, compress/flate %d bytes, ratio %.3f", kind, ours, buf.Len(), ratio)
		if ratio > 1.1 {
			t.Errorf("kind %d: %.3f of the size of compress/flate, want at most 1.1", kind, ratio)
		}
	}
}

type failingWriter struct {
	left int
	err  error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.left < len(p) {
		n := f.left
		f.left = 0
		return n, f.err
	}
	f.left -= len(p)
	return len(p), nil
}

// TestSinkErrors: błąd zapisu wraca niezmieniony i jest trwały; zapis krótszy bez błędu to io.ErrShortWrite.
// REQ: ZW-009
func TestSinkErrors(t *testing.T) {
	boom := errors.New("boom")
	data := sample(1, 300000)
	for _, tc := range []struct {
		sink *failingWriter
		want error
	}{{&failingWriter{left: 10, err: boom}, boom}, {&failingWriter{left: 70000, err: boom}, boom}, {&failingWriter{left: 10}, io.ErrShortWrite}} {
		w := NewWriter(tc.sink)
		_, err := w.Write(data)
		if err == nil {
			err = w.Close()
		}
		if err != tc.want {
			t.Errorf("got %v, want %v", err, tc.want)
		}
		if _, err2 := w.Write([]byte("x")); err2 != tc.want {
			t.Errorf("after the error Write gives %v, want %v", err2, tc.want)
		}
		if err2 := w.Close(); err2 != tc.want {
			t.Errorf("after the error Close gives %v, want %v", err2, tc.want)
		}
	}
}

// TestNoAllocations: koder nie alokuje w czasie pracy.
// REQ: ZW-005
func TestNoAllocations(t *testing.T) {
	data := sample(2, 500000)
	w := NewWriter(io.Discard)
	if n := testing.AllocsPerRun(5, func() {
		w.Reset(io.Discard)
		w.Write(data)
		w.Close()
	}); n != 0 {
		t.Errorf("%v allocations per stream, want 0", n)
	}
}

// FuzzDeflate: dla dowolnych danych i dowolnego podziału na zapisy strumień jest poprawny (oba dekodery oddają
// dane) i taki sam jak przy jednym zapisie.
// REQ: ZW-006 ZW-008
func FuzzDeflate(f *testing.F) {
	f.Add([]byte(nil), uint16(1))
	f.Add([]byte("abcabcabcabcabcabc"), uint16(3))
	f.Add(sample(2, 3000), uint16(700))
	f.Add(sample(3, 70000), uint16(65535))
	f.Fuzz(func(t *testing.T, data []byte, chunk uint16) {
		// dane fuzzera są krótkie; powielenie daje wejście dłuższe niż okno i niż blok
		long := bytes.Repeat(data, 1+int(chunk)%7)
		whole := compress(t, long)
		if got := expand(t, whole); !bytes.Equal(got, long) {
			t.Fatal("own decoder gives different data")
		}
		if got := expandStdlib(t, whole); !bytes.Equal(got, long) {
			t.Fatal("compress/flate gives different data")
		}
		if split := compress(t, long, int(chunk)+1); !bytes.Equal(split, whole) {
			t.Fatal("output depends on how the input is split")
		}
	})
}

func BenchmarkDeflate(b *testing.B) {
	for _, kind := range []int{2, 5, 1, 0} {
		data := sample(kind, 8<<20)
		b.Run(fmt.Sprint("ours/kind", kind), func(b *testing.B) {
			w := NewWriter(io.Discard)
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				w.Reset(io.Discard)
				w.Write(data)
				w.Close()
			}
		})
		b.Run(fmt.Sprint("stdlib/kind", kind), func(b *testing.B) {
			w, _ := flate.NewWriter(io.Discard, flate.DefaultCompression)
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				w.Reset(io.Discard)
				w.Write(data)
				w.Close()
			}
		})
	}
}
