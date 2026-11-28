package inflate

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"math/rand"
	"testing"
)

// randomCode: zupełny kod Huffmana dla n symboli (n>=2), długości <= 15, z naciskiem na długie kody.
func randomCode(rng *rand.Rand, n int, deep bool) []int {
	leaves := []int{0}
	for len(leaves) < n {
		var i int
		if deep && rng.Intn(4) != 0 {
			// rozdziel najgłębszy liść < 15
			best := -1
			for j, d := range leaves {
				if d < 15 && (best < 0 || d > leaves[best]) {
					best = j
				}
			}
			i = best
		} else {
			i = rng.Intn(len(leaves))
			for leaves[i] >= 15 {
				i = rng.Intn(len(leaves))
			}
		}
		leaves[i]++
		leaves = append(leaves, leaves[i])
	}
	rng.Shuffle(len(leaves), func(a, b int) { leaves[a], leaves[b] = leaves[b], leaves[a] })
	return leaves
}

type gen struct {
	rng  *rand.Rand
	w    bitWriter
	want []byte
}

// kod długości kodów: 13 symboli po 4 bity, 6 po 5 bitów.
var clLens = func() []int {
	l := make([]int, 19)
	for i := range l {
		if i < 13 {
			l[i] = 4
		} else {
			l[i] = 5
		}
	}
	return l
}()

func (g *gen) header(final bool, lit, dist []int, hlit, hdist int) {
	f := uint32(0)
	if final {
		f = 1
	}
	g.w.bits(f, 1).bits(2, 2).bits(uint32(hlit-257), 5).bits(uint32(hdist-1), 5).bits(19-4, 4)
	// permutacja długości kodu długości kodów, żeby tablice różniły się między blokami
	perm := append([]int{}, clLens...)
	g.rng.Shuffle(19, func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
	for _, s := range codeLengthOrder {
		g.w.bits(uint32(perm[s]), 3)
	}
	codes := canonical(perm)
	all := append(append([]int{}, lit[:hlit]...), dist[:hdist]...)
	for i := 0; i < len(all); {
		run := 1
		for i+run < len(all) && all[i+run] == all[i] {
			run++
		}
		switch {
		case all[i] == 0 && run >= 11 && g.rng.Intn(3) != 0:
			r := min(run, 138)
			g.w.code(codes[18], uint(perm[18])).bits(uint32(r-11), 7)
			i += r
		case all[i] == 0 && run >= 3 && g.rng.Intn(3) != 0:
			r := min(run, 10)
			g.w.code(codes[17], uint(perm[17])).bits(uint32(r-3), 3)
			i += r
		case i > 0 && all[i-1] == all[i] && run >= 3 && g.rng.Intn(3) != 0:
			r := min(run, 6)
			g.w.code(codes[16], uint(perm[16])).bits(uint32(r-3), 2)
			i += r
		default:
			g.w.code(codes[all[i]], uint(perm[all[i]]))
			i++
		}
	}
}

func (g *gen) stored(final bool, n int) {
	f := uint32(0)
	if final {
		f = 1
	}
	g.w.bits(f, 1).bits(0, 2)
	for g.w.n%8 != 0 {
		g.w.bits(uint32(g.rng.Intn(2)), 1)
	}
	g.w.bits(uint32(n), 16).bits(uint32(n)^0xFFFF, 16)
	for i := 0; i < n; i++ {
		b := byte(g.rng.Intn(256))
		g.w.bits(uint32(b), 8)
		g.want = append(g.want, b)
	}
}

// block: blok z kodami; bad>0 wstawia wadliwy symbol.
func (g *gen) block(final bool, fixed bool, ntok int) {
	rng := g.rng
	lit, dist := make([]int, 288), make([]int, 32)
	hlit, hdist := 286, 30
	distMode := 0 // 0 zupełny, 1 jeden kod, 2 pusty
	if fixed {
		for s := range lit {
			_, n := fixedLitCode(s)
			lit[s] = int(n)
		}
		for s := range dist {
			dist[s] = 5
		}
		f := uint32(0)
		if final {
			f = 1
		}
		g.w.bits(f, 1).bits(1, 2)
	} else {
		hlit = 257 + rng.Intn(30)
		hdist = 1 + rng.Intn(30)
		// wybór używanych symboli
		used := []int{endOfBlock}
		for s := 0; s < hlit; s++ {
			if s != endOfBlock && rng.Intn(3) != 0 {
				used = append(used, s)
			}
		}
		if len(used) < 2 {
			used = append(used, 0)
		}
		ls := randomCode(rng, len(used), rng.Intn(2) == 0)
		for i, s := range used {
			lit[s] = ls[i]
		}
		distMode = rng.Intn(6)
		if distMode > 2 {
			distMode = 0
		}
		switch distMode {
		case 0:
			var du []int
			for s := 0; s < hdist; s++ {
				if rng.Intn(4) != 0 {
					du = append(du, s)
				}
			}
			if len(du) < 2 {
				if hdist < 2 {
					hdist = 2
				}
				du = []int{0, hdist - 1}
			}
			ds := randomCode(rng, len(du), rng.Intn(2) == 0)
			for i, s := range du {
				dist[s] = ds[i]
			}
		case 1:
			dist[rng.Intn(hdist)] = 1
		}
		g.header(final, lit, dist, hlit, hdist)
	}
	litCode, distCode := canonical(lit[:]), canonical(dist[:])
	var lits, lens, dists []int
	for s := 0; s < 256; s++ {
		if lit[s] != 0 {
			lits = append(lits, s)
		}
	}
	for s := 257; s < 286; s++ {
		if lit[s] != 0 {
			lens = append(lens, s-257)
		}
	}
	for s := 0; s < 30; s++ {
		if dist[s] != 0 {
			dists = append(dists, s)
		}
	}
	for t := 0; t < ntok; t++ {
		canMatch := len(lens) > 0 && len(dists) > 0 && len(g.want) > 0
		if len(lits) > 0 && (!canMatch || rng.Intn(3) == 0) {
			s := lits[rng.Intn(len(lits))]
			g.w.code(litCode[s], uint(lit[s]))
			g.want = append(g.want, byte(s))
			continue
		}
		if !canMatch {
			break
		}
		// odległość: wybierz cel, potem symbol, który go mieści
		total := len(g.want)
		wp := total % windowSize
		var target int
		switch rng.Intn(8) {
		case 0:
			target = 32768
		case 1:
			target = 1 + rng.Intn(4)
		case 2:
			target = wp + rng.Intn(5) - 2 // źródło przy początku okna
		case 3:
			target = wp + 1 + rng.Intn(300)
		case 4:
			target = 32768 - rng.Intn(300)
		default:
			target = 1 + rng.Intn(32768)
		}
		target = max(1, min(target, 32768, total))
		// największy używany symbol odległości z bazą <= target
		d := -1
		for _, c := range dists {
			if int(distBase[c]) <= target {
				d = c
			}
		}
		if d < 0 {
			if len(lits) == 0 {
				break
			}
			s := lits[rng.Intn(len(lits))]
			g.w.code(litCode[s], uint(lit[s]))
			g.want = append(g.want, byte(s))
			continue
		}
		back := min(target, int(distBase[d])+1<<distExtra[d]-1)
		ls := lens[rng.Intn(len(lens))]
		if rng.Intn(3) == 0 {
			ls = lens[len(lens)-1]
		}
		lx := rng.Intn(1 << lengthExtra[ls])
		if rng.Intn(2) == 0 {
			lx = 1<<lengthExtra[ls] - 1
		}
		length := int(lengthBase[ls]) + lx
		g.w.code(litCode[257+ls], uint(lit[257+ls])).bits(uint32(lx), uint(lengthExtra[ls]))
		g.w.code(distCode[d], uint(dist[d])).bits(uint32(back-int(distBase[d])), uint(distExtra[d]))
		for i := 0; i < length; i++ {
			g.want = append(g.want, g.want[len(g.want)-back])
		}
	}
	g.w.code(litCode[endOfBlock], uint(lit[endOfBlock]))
}

func genStream(rng *rand.Rand) ([]byte, []byte) {
	g := &gen{rng: rng}
	nb := 1 + rng.Intn(5)
	if rng.Intn(2) == 0 {
		g.stored(false, rng.Intn(4200)) // przesuwa resztę względem bufora wejścia
	}
	for b := 0; b < nb; b++ {
		final := b == nb-1
		switch rng.Intn(8) {
		case 0:
			n := rng.Intn(300)
			if rng.Intn(4) == 0 {
				n = rng.Intn(65536)
			}
			g.stored(final, n)
		case 1, 2:
			g.block(final, true, rng.Intn(3000))
		default:
			n := rng.Intn(3000)
			if rng.Intn(5) == 0 {
				n = rng.Intn(40)
			}
			g.block(final, false, n)
		}
	}
	return g.w.out, g.want
}

// randSrc: odczyty losowej wielkości, opcjonalnie błąd na końcu razem z danymi.
type randSrc struct {
	data []byte
	rng  *rand.Rand
	max  int
	err  error
	with bool
}

func (c *randSrc) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		return 0, io.EOF
	}
	if c.rng.Intn(10) == 0 {
		return 0, nil
	}
	k := min(1+c.rng.Intn(c.max), len(p), len(c.data))
	copy(p, c.data[:k])
	c.data = c.data[k:]
	if len(c.data) == 0 && c.with && c.err != nil {
		return k, c.err
	}
	return k, nil
}

func drain(r *Reader, buf int) outcome {
	var o outcome
	p := make([]byte, buf)
	for len(o.out) < 1<<24 {
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

// TestRandomStreams: losowe strumienie o wielu blokach (niezakodowane, ze stałymi kodami i z losowymi zupełnymi
// kodami dynamicznymi do 15 bitów; kody odległości zupełne, z jednym kodem albo puste; odwołania z naciskiem na
// odległość 32 768, odległości 1–4, źródła przechodzące przez początek i koniec okna i największe długości)
// oraz ich wersje ucięte i uszkodzone. Poprawny strumień daje dokładnie to, co wyznaczył generator, a co
// dwudziesty jest sprawdzany także przez compress/flate; szybka ścieżka daje to samo co zwykła przy różnych
// podziałach wejścia i wyjścia, ze źródłem o losowych odczytach i błędzie oraz na jednym dekoderze używanym
// przez Reset dla wszystkich strumieni. Generator i test pochodzą z niezależnego przeglądu szybkiej ścieżki
// (ADR 0008).
// REQ: INF-010 INF-001 INF-008
func TestRandomStreams(t *testing.T) {
	rng := rand.New(rand.NewSource(20261127))
	reused := NewReader(nil)
	errBoom := errors.New("boom")
	valid, invalid := 0, 0
	for iter := 0; iter < 300; iter++ {
		stream, want := genStream(rng)
		// wyrocznia dla poprawnych
		ref := decodeWith(stream, true, 1<<20, 32<<10, 1<<30)
		if ref.kind != 0 || ref.other != "" || !bytes.Equal(ref.out, want) || ref.consumed != int64(len(stream)) {
			t.Fatalf("iter %d: plain path wrong on a valid stream: kind %d at %d, %d bytes want %d, consumed %d of %d", iter, ref.kind, ref.offset, len(ref.out), len(want), ref.consumed, len(stream))
		}
		if iter%20 == 0 {
			theirs, err := io.ReadAll(flate.NewReader(bytes.NewReader(stream)))
			if err != nil || !bytes.Equal(theirs, want) {
				t.Fatalf("iter %d: generator disagrees with compress/flate: %v", iter, err)
			}
		}
		variants := [][]byte{stream}
		for m := 0; m < 6; m++ {
			v := append([]byte{}, stream...)
			switch rng.Intn(4) {
			case 0:
				v = v[:rng.Intn(len(v)+1)]
			case 1:
				if len(v) > 0 {
					v[rng.Intn(len(v))] ^= 1 << rng.Intn(8)
				}
			case 2:
				if len(v) > 0 {
					// uszkodzenie blisko końca: błąd trafia na szybką ścieżkę po długim biegu
					i := len(v) - 1 - rng.Intn(min(len(v), 200))
					v[i] ^= 1 << rng.Intn(8)
				}
			default:
				if len(v) > 0 {
					i := rng.Intn(len(v))
					for j := i; j < len(v) && j < i+1+rng.Intn(4); j++ {
						v[j] = byte(rng.Intn(256))
					}
				}
			}
			variants = append(variants, v)
		}
		for vi, v := range variants {
			want := decodeWith(v, true, 1<<20, 32<<10, 1<<30)
			if want.kind == 0 {
				valid++
			} else {
				invalid++
			}
			chunks := []int{1 << 20, 4096, 4095, 9, 8, 1 + rng.Intn(40), 4000 + rng.Intn(200)}
			bufs := []int{32 << 10, 1 + rng.Intn(600), 258, 32767, 32769}
			for _, chunk := range chunks {
				buf := bufs[rng.Intn(len(bufs))]
				if got := decodeWith(v, false, chunk, buf, 1<<30); !got.equal(want) {
					t.Fatalf("iter %d variant %d chunk %d buf %d: fast %d bytes kind %d at %d consumed %d other %q; plain %d bytes kind %d at %d consumed %d other %q\nstream %x",
						iter, vi, chunk, buf, len(got.out), got.kind, got.offset, got.consumed, got.other, len(want.out), want.kind, want.offset, want.consumed, want.other, v)
				}
			}
			// źródło z losowymi odczytami i błędem, obie ścieżki z tym samym ziarnem
			seed := rng.Int63()
			mx := 1 + rng.Intn(5000)
			a := NewReader(&randSrc{data: v, rng: rand.New(rand.NewSource(seed)), max: mx, err: errBoom, with: seed%2 == 0})
			b := NewReader(&randSrc{data: v, rng: rand.New(rand.NewSource(seed)), max: mx, err: errBoom, with: seed%2 == 0})
			b.slow = true
			oa, ob := drain(a, 777), drain(b, 777)
			if !oa.equal(ob) {
				t.Fatalf("iter %d variant %d random source max %d: fast %d bytes kind %d at %d consumed %d other %q; plain %d bytes kind %d at %d consumed %d other %q",
					iter, vi, mx, len(oa.out), oa.kind, oa.offset, oa.consumed, oa.other, len(ob.out), ob.kind, ob.offset, ob.consumed, ob.other)
			}
			if want.kind == 0 && want.other == "" && (!bytes.Equal(oa.out, want.out) || oa.kind != 0 || oa.consumed != want.consumed) {
				t.Fatalf("iter %d variant %d: random source changes the result", iter, vi)
			}
			// ten sam Reader przez Reset, z tablicami i oknem po poprzednich strumieniach
			reused.Reset(&pieces{v, chunks[rng.Intn(len(chunks))]})
			if got := drain(reused, 5000); !got.equal(want) {
				t.Fatalf("iter %d variant %d: reused reader: fast %d bytes kind %d at %d consumed %d; plain %d bytes kind %d at %d consumed %d",
					iter, vi, len(got.out), got.kind, got.offset, got.consumed, len(want.out), want.kind, want.offset, want.consumed)
			}
		}
	}
	t.Logf("valid %d, invalid %d", valid, invalid)
}
