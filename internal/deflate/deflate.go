// Package deflate koduje strumień DEFLATE według RFC 1951. Napisany z tekstu RFC (format bloków w rozdziale 3,
// opis dopasowań przez łańcuchy skrótów i odroczonego wyboru w rozdziale 4) i z opisu algorytmu package-merge
// (Larmore, Hirschberg, 1990) dla kodów o ograniczonej długości; compress/flate służy tylko jako wyrocznia
// w testach (docs/provenance.md).
//
// Algorytm i jego parametry są stałe: te same dane dają te same bajty na każdym systemie, w każdej architekturze
// i w każdej wersji Go, niezależnie od tego, jak wywołujący dzieli dane na wywołania Write. Zmiana którejkolwiek
// stałej z tego pliku zmienia bajty wyjścia, więc jest zmianą formatu wyniku biblioteki (ADR 0005).
//
// Koder ma stałą pamięć (bufor wejścia, tablice skrótów, symbole bloku) i nie alokuje w czasie pracy.
package deflate

import (
	"encoding/binary"
	"io"
	"math/bits"
)

// Parametry algorytmu. Każdy wpływa na bajty wyjścia.
const (
	windowSize = 32768 // największa odległość dopasowania (RFC 1951, 3.2.5)
	minMatch   = 3
	maxMatch   = 258

	bufSize  = 4 * windowSize // bufor wejścia: okno, bieżący blok i dane do przodu
	hashBits = 15
	hashLen  = 4          // skrót liczymy z czterech bajtów: łańcuch nie zbiera przypadkowych trafień na trzech
	hashMul  = 0x9E3779B1 // 2^32 / złoty podział: mnożnik skrótu

	// Blok kończy się po maxTokens symbolach albo gdy następny symbol mógłby wyprowadzić go ponad maxBlockBytes
	// bajtów wejścia. Dzięki temu blok mieści się w jednym bloku niezakodowanym, a jego dane są jeszcze w buforze.
	maxTokens     = 16384
	maxBlockBytes = 65535

	// Pozycja jest kodowana, gdy ma przed sobą tyle bajtów (albo strumień się kończy): najdłuższe dopasowanie
	// i bajty skrótu ostatniej pozycji, którą ono obejmuje.
	lookahead = maxMatch + hashLen + 1

	maxChain   = 32   // ile kandydatów z łańcucha skrótu sprawdzamy
	goodLength = 8    // od tej długości odroczonego dopasowania szukamy lepszego już tylko w ćwierci łańcucha
	lazyLength = 64   // od tej długości dopasowanie jest wydawane bez sprawdzenia następnej pozycji
	farLimit   = 4096 // dopasowanie długości 3 dalej niż tyle kosztuje więcej niż trzy literały
	tailInsert = 64   // z pozycji objętych dopasowaniem do łańcuchów trafia tylko tyle ostatnich

	// Dane bez dopasowań: po każdych 2^missShift kolejnych pozycjach bez trafienia o jedną pozycję więcej idzie
	// literałem bez szukania, najwyżej maxSkip naraz. Pierwsze trafienie zeruje licznik.
	missShift = 5
	maxSkip   = 8

	endOfBlock  = 256
	numLitLen   = 286
	numDist     = 30
	numCodeLen  = 19
	maxCodeBits = 15
	maxCLBits   = 7
)

// Kolejność długości kodu długości w nagłówku bloku dynamicznego (RFC 1951, 3.2.7).
var codeLenOrder = [numCodeLen]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

// lengthSymbol i lengthExtra: dla długości dopasowania pomniejszonej o 3 numer symbolu ponad 257 i liczba bitów
// dodatkowych (RFC 1951, 3.2.5). Wyliczone z reguły tabeli: po ośmiu długościach bez bitów dodatkowych każde
// cztery kolejne symbole mają o jeden bit więcej; długość 258 ma własny symbol 285.
var lengthSymbol, lengthExtra = func() (sym, extra [256]uint8) {
	for v := 0; v < 256; v++ {
		switch {
		case v < 8:
			sym[v] = uint8(v)
		case v == 255:
			sym[v] = 28
		default:
			n := bits.Len(uint(v)) - 1
			sym[v] = uint8(4*(n-1) + v>>(n-2)&3)
			extra[v] = uint8(n - 2)
		}
	}
	return
}()

// distSymbol zwraca symbol odległości i liczbę jej bitów dodatkowych (RFC 1951, 3.2.5): cztery pierwsze
// odległości mają własne symbole, potem każde dwa kolejne symbole mają o jeden bit dodatkowy więcej.
func distSymbol(dist int) (sym, extra uint) {
	d := uint(dist - 1)
	if d < 4 {
		return d, 0
	}
	n := uint(bits.Len(d)) - 1
	return 2*n + d>>(n-1)&1, n - 1
}

// Kody stałe (RFC 1951, 3.2.6).
var fixedLitLen, fixedDist = func() (l, d code) {
	for i := range l.length[:numLitLen+2] {
		switch {
		case i < 144:
			l.length[i] = 8
		case i < 256:
			l.length[i] = 9
		case i < 280:
			l.length[i] = 7
		default:
			l.length[i] = 8
		}
	}
	l.assign(numLitLen + 2)
	for i := 0; i < 32; i++ {
		d.length[i] = 5
	}
	d.assign(32)
	return
}()

// code to kod Huffmana alfabetu o najwyżej 288 symbolach: długości i słowa kodowe z odwróconą kolejnością bitów,
// gotowe do zapisu od najmłodszego bitu.
type code struct {
	length [288]uint8
	word   [288]uint16
}

// assign nadaje słowa kodowe długościom pierwszych n symboli (RFC 1951, 3.2.2).
func (c *code) assign(n int) {
	var count, next [maxCodeBits + 1]uint16
	for _, l := range c.length[:n] {
		count[l]++
	}
	count[0] = 0
	w := uint16(0)
	for l := 1; l <= maxCodeBits; l++ {
		w = (w + count[l-1]) << 1
		next[l] = w
	}
	for i, l := range c.length[:n] {
		if l == 0 {
			c.word[i] = 0
			continue
		}
		c.word[i] = bits.Reverse16(next[l]) >> (16 - l)
		next[l]++
	}
}

// build wyznacza długości kodu o najmniejszym koszcie dla częstości freq, nie dłuższe niż limit bitów, i nadaje
// słowa. Kod jest zawsze pełny: gdy używany jest najwyżej jeden symbol, długość 1 dostaje także drugi symbol
// (o najniższym numerze), bo kod z jednym słowem jednobitowym nie każdy dekoder przyjmuje.
//
// Algorytm package-merge: lista poziomu 1 to symbole uporządkowane rosnąco według (częstość, numer); lista
// poziomu l to scalenie tych samych symboli z parami kolejnych pozycji listy poziomu l−1. Z listy ostatniego
// poziomu bierzemy 2n−2 pierwszych pozycji; długość kodu symbolu to liczba poziomów, na których został wzięty.
// Symbole są brane od początku uporządkowania, więc na każdym poziomie wystarczy policzyć, ile ich wzięto.
// Remisy rozstrzyga stała reguła (symbol przed parą), więc wynik zależy tylko od częstości.
func (c *code) build(freq []int32, limit int) {
	var syms [numLitLen]uint16
	var weight [numLitLen]int64
	n := 0
	for i, f := range freq {
		c.length[i] = 0
		if f > 0 {
			// wstawianie do uporządkowanej listy; numery rosną, więc przy równej częstości nowy idzie na koniec
			k := n
			for k > 0 && weight[k-1] > int64(f) {
				syms[k], weight[k] = syms[k-1], weight[k-1]
				k--
			}
			syms[k], weight[k] = uint16(i), int64(f)
			n++
		}
	}
	switch n {
	case 0:
		c.length[0], c.length[1] = 1, 1
	case 1:
		c.length[syms[0]] = 1
		if syms[0] == 0 {
			c.length[1] = 1
		} else {
			c.length[0] = 1
		}
	default:
		take := 2*n - 2
		// leaf[l][k]: czy pozycja k listy poziomu l jest symbolem (a nie parą). Listy są ucięte do take pozycji.
		var leaf [maxCodeBits][2 * numLitLen]bool
		var prev, cur [2 * numLitLen]int64
		prevLen := 0
		for l := 0; l < limit; l++ {
			pairs := prevLen / 2
			i, p, k := 0, 0, 0
			for k < take && (i < n || p < pairs) {
				var pw int64
				if p < pairs {
					pw = prev[2*p] + prev[2*p+1]
				}
				if i < n && (p >= pairs || weight[i] <= pw) {
					cur[k], leaf[l][k] = weight[i], true
					i++
				} else {
					cur[k], leaf[l][k] = pw, false
					p++
				}
				k++
			}
			prev, prevLen = cur, k
		}
		for l := limit - 1; l >= 0 && take > 0; l-- {
			leaves := 0
			for k := 0; k < take; k++ {
				if leaf[l][k] {
					leaves++
				}
			}
			for i := 0; i < leaves; i++ {
				c.length[syms[i]]++
			}
			take = 2 * (take - leaves)
		}
	}
	c.assign(len(freq))
}

// Writer koduje jeden strumień DEFLATE zapisywany do dst.
type Writer struct {
	dst io.Writer
	err error

	buf        []byte // dane wejściowe: [0, n)
	n          int
	pos        int // następna pozycja do zakodowania
	blockStart int // początek danych bieżącego bloku
	blockLen   int // bajty wejścia objęte symbolami bieżącego bloku

	// head[skrót] i prev[pozycja mod okno] trzymają pozycje w buf powiększone o 1; 0 to brak.
	head []int32
	prev []int32

	// Odroczone dopasowanie znalezione na pozycji pos−1.
	pending           bool
	misses            int // kolejne pozycje bez dopasowania
	skipping          int // ile następnych pozycji idzie literałem bez szukania
	pendLen, pendDist int

	tokens   []uint32 // literał: bajt; dopasowanie: 1<<31 | (długość−3)<<16 | (odległość−1)
	litFreq  [numLitLen]int32
	distFreq [numDist]int32

	lit, dist, cl code

	// Długości kodów bloku dynamicznego zakodowane symbolami 0–18; bity dodatkowe w starszym bajcie.
	clSyms [numLitLen + numDist]uint16
	clFreq [numCodeLen]int32

	acc   uint64 // bity czekające na zapis, od najmłodszego
	nbits uint
	out   []byte
}

// NewWriter zwraca koder strumienia zapisywanego do dst.
func NewWriter(dst io.Writer) *Writer {
	return &Writer{
		dst:    dst,
		buf:    make([]byte, bufSize),
		head:   make([]int32, 1<<hashBits),
		prev:   make([]int32, windowSize),
		tokens: make([]uint32, 0, maxTokens),
		out:    make([]byte, 0, 1<<15),
	}
}

// Reset przygotowuje koder do nowego strumienia zapisywanego do dst, bez nowej alokacji. Wynik nie zależy od
// tego, co koder kodował wcześniej.
func (w *Writer) Reset(dst io.Writer) {
	w.dst, w.err = dst, nil
	w.n, w.pos, w.blockStart, w.blockLen = 0, 0, 0, 0
	clear(w.head) // prev jest czytane tylko dla pozycji osiągniętych z head, więc nie wymaga czyszczenia
	w.pending, w.misses, w.skipping = false, 0, 0
	w.tokens = w.tokens[:0]
	w.litFreq, w.distFreq = [numLitLen]int32{}, [numDist]int32{}
	w.acc, w.nbits, w.out = 0, 0, w.out[:0]
}

// Write przyjmuje kolejne bajty wejścia. Zwraca błąd dst, jeśli zapis się nie udał; błąd jest trwały.
func (w *Writer) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 && w.err == nil {
		if w.n == bufSize {
			w.compress(false)
			w.slide()
		}
		k := copy(w.buf[w.n:], p)
		w.n += k
		p = p[k:]
		total += k
	}
	return total, w.err
}

// Close koduje resztę danych i kończy strumień. Nie zamyka dst.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	w.compress(true)
	if w.pending {
		w.match(w.pendLen, w.pendDist)
		w.pending = false
	}
	w.block(true)
	// dopełnienie ostatniego bajtu zerami
	for w.nbits > 0 {
		w.out = append(w.out, byte(w.acc))
		w.acc >>= 8
		w.nbits -= min(w.nbits, 8)
	}
	w.flush()
	return w.err
}

// slide przesuwa bufor o wielokrotność okna, zostawiając okno przed bieżącą pozycją i dane bieżącego bloku.
// Wywoływane przy pełnym buforze po compress(false), więc jest o co przesunąć.
func (w *Writer) slide() {
	shift := min(w.blockStart, w.pos-windowSize) / windowSize * windowSize
	if shift <= 0 {
		return
	}
	copy(w.buf, w.buf[shift:w.n])
	w.n -= shift
	w.pos -= shift
	w.blockStart -= shift
	s := int32(shift)
	for i, v := range w.head {
		w.head[i] = max(v-s, 0)
	}
	for i, v := range w.prev {
		w.prev[i] = max(v-s, 0)
	}
}

func (w *Writer) hash(p int) uint32 {
	return binary.LittleEndian.Uint32(w.buf[p:]) * hashMul >> (32 - hashBits)
}

// insert dopisuje pozycję p do łańcucha jej skrótu.
func (w *Writer) insert(p int) {
	h := w.hash(p)
	w.prev[p&(windowSize-1)] = w.head[h]
	w.head[h] = int32(p + 1)
}

// compress koduje pozycje, dla których w buforze jest pełny zapas danych do przodu; z final=true wszystkie.
// Decyzja o każdej pozycji zależy tylko od danych, nie od chwili wywołania.
func (w *Writer) compress(final bool) {
	for w.pos < w.n && w.err == nil {
		p := w.pos
		avail := w.n - p
		if !final && avail < lookahead {
			return
		}
		if w.skipping > 0 {
			w.skipping--
			w.literal(w.buf[p])
			w.pos++
			continue
		}
		length, dist := 0, 0
		if avail >= hashLen {
			floor := minMatch - 1
			if w.pending {
				floor = w.pendLen
			}
			if floor < min(avail, maxMatch) && floor < lazyLength {
				length, dist = w.longest(p, min(avail, maxMatch), floor)
			}
			w.insert(p)
		}
		switch {
		case w.pending && length > w.pendLen:
			// lepsze dopasowanie o pozycję dalej: poprzednia pozycja zostaje literałem (RFC 1951, rozdz. 4)
			w.literal(w.buf[p-1])
			w.pendLen, w.pendDist = length, dist
			w.pos++
		case w.pending:
			end := p - 1 + w.pendLen
			w.match(w.pendLen, w.pendDist)
			w.pending = false
			w.skip(p+1, end)
		case length >= lazyLength:
			w.match(length, dist)
			w.skip(p+1, p+length)
		case length >= minMatch:
			w.pending, w.pendLen, w.pendDist = true, length, dist
			w.misses = 0
			w.pos++
		default:
			w.literal(w.buf[p])
			w.pos++
			w.misses++
			w.skipping = min(w.misses>>missShift, maxSkip)
		}
	}
}

// skip dopisuje do łańcuchów ostatnie pozycje z [from, end) objęte dopasowaniem i ustawia bieżącą pozycję na end.
func (w *Writer) skip(from, end int) {
	w.misses = 0
	from = max(from, end-tailInsert)
	for q := from; q < end && q+hashLen <= w.n; q++ {
		w.insert(q)
	}
	w.pos = end
}

// longest szuka w łańcuchu skrótu pozycji p najdłuższego dopasowania dłuższego niż floor i nie dłuższego niż
// limit. Z dopasowań tej samej długości wygrywa najbliższe (pierwsze w łańcuchu).
func (w *Writer) longest(p, limit, floor int) (length, dist int) {
	chain := maxChain
	if floor >= goodLength {
		chain >>= 2
	}
	buf := w.buf
	lowest := p - windowSize
	best := floor
	// Dopasowanie dłuższe niż best zgadza się w szczególności na czterech bajtach kończących się na pozycji
	// best: jedno porównanie odrzuca większość kandydatów. Przy best = 2 są to trzy pierwsze bajty.
	at := max(best-3, 0)
	want := tail(buf, p+at, best)
	for c := w.head[w.hash(p)]; c > 0 && chain > 0; chain-- {
		cand := int(c) - 1
		if cand < lowest {
			break
		}
		if tail(buf, cand+at, best) == want {
			l := 0
			for l+8 <= limit {
				if x := binary.LittleEndian.Uint64(buf[cand+l:]) ^ binary.LittleEndian.Uint64(buf[p+l:]); x != 0 {
					l += bits.TrailingZeros64(x) >> 3
					break
				}
				l += 8
			}
			if l+8 > limit {
				for l < limit && buf[cand+l] == buf[p+l] {
					l++
				}
			}
			if l > best {
				best, dist = l, p-cand
				if l >= limit {
					break
				}
				at = best - 3
				want = tail(buf, p+at, best)
			}
		}
		c = w.prev[cand&(windowSize-1)]
	}
	if dist == 0 || best == minMatch && dist > farLimit {
		return 0, 0
	}
	return best, dist
}

// tail zwraca cztery bajty od pozycji i, a przy best = 2 trzy (czwarty mógłby leżeć poza danymi).
func tail(buf []byte, i, best int) uint32 {
	if best < 3 {
		b := buf[i : i+3 : i+3]
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
	}
	return binary.LittleEndian.Uint32(buf[i:])
}

// room kończy bieżący blok, gdy nie zmieści on kolejnego symbolu.
func (w *Writer) room() {
	if len(w.tokens) == maxTokens || w.blockLen > maxBlockBytes-maxMatch {
		w.block(false)
	}
}

func (w *Writer) literal(b byte) {
	w.room()
	w.tokens = append(w.tokens, uint32(b))
	w.litFreq[b]++
	w.blockLen++
}

func (w *Writer) match(length, dist int) {
	w.room()
	w.tokens = append(w.tokens, 1<<31|uint32(length-minMatch)<<16|uint32(dist-1))
	w.litFreq[257+int(lengthSymbol[length-minMatch])]++
	ds, _ := distSymbol(dist)
	w.distFreq[ds]++
	w.blockLen += length
}

// block zapisuje symbole bieżącego bloku w najtańszej z trzech postaci (RFC 1951, 3.2.3): niezakodowanej, ze
// stałymi kodami albo z kodami dynamicznymi. Przy równym koszcie wygrywa postać wcześniejsza na tej liście.
func (w *Writer) block(final bool) {
	if w.err != nil {
		return
	}
	w.litFreq[endOfBlock]++
	w.lit.build(w.litFreq[:], maxCodeBits)
	w.dist.build(w.distFreq[:], maxCodeBits)
	hlit, hdist := numLitLen, numDist
	for hlit > 257 && w.lit.length[hlit-1] == 0 {
		hlit--
	}
	for hdist > 1 && w.dist.length[hdist-1] == 0 {
		hdist--
	}
	nsyms := w.runLengths(hlit, hdist)
	w.cl.build(w.clFreq[:], maxCLBits)
	hclen := numCodeLen
	for hclen > 4 && w.cl.length[codeLenOrder[hclen-1]] == 0 {
		hclen--
	}

	// Koszt w bitach, bez trzech bitów nagłówka bloku.
	dynamic := int64(14 + 3*hclen)
	for s, f := range w.clFreq {
		dynamic += int64(f) * int64(w.cl.length[s])
	}
	dynamic += int64(w.clFreq[16])*2 + int64(w.clFreq[17])*3 + int64(w.clFreq[18])*7
	fixed := int64(0)
	for s, f := range w.litFreq {
		extra := int64(0)
		if s > 264 && s < 285 {
			extra = int64(s-261) / 4
		}
		dynamic += int64(f) * (int64(w.lit.length[s]) + extra)
		fixed += int64(f) * (int64(fixedLitLen.length[s]) + extra)
	}
	for s, f := range w.distFreq {
		extra := int64(0)
		if s > 3 {
			extra = int64(s-2) / 2
		}
		dynamic += int64(f) * (int64(w.dist.length[s]) + extra)
		fixed += int64(f) * (5 + extra)
	}
	stored := int64((8-(w.nbits+3)%8)%8) + 32 + 8*int64(w.blockLen)

	last := uint64(0)
	if final {
		last = 1
	}
	switch {
	case stored <= fixed && stored <= dynamic:
		w.bits(last, 3) // BTYPE 00
		if pad := (8 - w.nbits%8) % 8; pad > 0 {
			w.bits(0, pad)
		}
		w.bits(uint64(w.blockLen), 16)
		w.bits(uint64(^uint16(w.blockLen)), 16)
		for w.nbits > 0 {
			w.out = append(w.out, byte(w.acc))
			w.acc >>= 8
			w.nbits -= 8
		}
		w.flush()
		w.write(w.buf[w.blockStart : w.blockStart+w.blockLen])
	case fixed <= dynamic:
		w.bits(last|1<<1, 3) // BTYPE 01
		w.symbols(&fixedLitLen, &fixedDist)
	default:
		w.bits(last|2<<1, 3) // BTYPE 10
		w.bits(uint64(hlit-257), 5)
		w.bits(uint64(hdist-1), 5)
		w.bits(uint64(hclen-4), 4)
		for _, s := range codeLenOrder[:hclen] {
			w.bits(uint64(w.cl.length[s]), 3)
		}
		for _, v := range w.clSyms[:nsyms] {
			s := v & 0xFF
			w.bits(uint64(w.cl.word[s]), uint(w.cl.length[s]))
			switch s {
			case 16:
				w.bits(uint64(v>>8), 2)
			case 17:
				w.bits(uint64(v>>8), 3)
			case 18:
				w.bits(uint64(v>>8), 7)
			}
		}
		w.symbols(&w.lit, &w.dist)
	}
	w.blockStart += w.blockLen
	w.blockLen = 0
	w.tokens = w.tokens[:0]
	w.litFreq, w.distFreq = [numLitLen]int32{}, [numDist]int32{}
}

// runLengths koduje długości kodów literałów i odległości jako jeden ciąg symboli 0–18 (RFC 1951, 3.2.7), liczy
// częstości tych symboli i zwraca ich liczbę. Reguła jest stała: seria zer idzie symbolem 18 (11–138), potem 17
// (3–10), reszta pojedynczo; seria innej długości zaczyna się od niej samej, potem symbol 16 (3–6), reszta
// pojedynczo.
func (w *Writer) runLengths(hlit, hdist int) int {
	w.clFreq = [numCodeLen]int32{}
	total := hlit + hdist
	at := func(i int) uint8 {
		if i < hlit {
			return w.lit.length[i]
		}
		return w.dist.length[i-hlit]
	}
	n := 0
	emit := func(sym, extra int) {
		w.clSyms[n] = uint16(sym | extra<<8)
		w.clFreq[sym]++
		n++
	}
	for i := 0; i < total; {
		v := at(i)
		run := 1
		for i+run < total && at(i+run) == v {
			run++
		}
		i += run
		if v == 0 {
			for ; run >= 11; run -= min(run, 138) {
				emit(18, min(run, 138)-11)
			}
			if run >= 3 {
				emit(17, run-3)
				run = 0
			}
		} else {
			emit(int(v), 0)
			for run--; run >= 3; run -= min(run, 6) {
				emit(16, min(run, 6)-3)
			}
		}
		for ; run > 0; run-- {
			emit(int(v), 0)
		}
	}
	return n
}

// symbols zapisuje symbole bloku podanymi kodami i znak końca bloku.
func (w *Writer) symbols(lit, dist *code) {
	for _, t := range w.tokens {
		if t < 1<<31 {
			w.bits(uint64(lit.word[t]), uint(lit.length[t]))
			continue
		}
		lv := t >> 16 & 0xFF
		ls := 257 + uint(lengthSymbol[lv])
		le := uint(lengthExtra[lv])
		// słowo długości (najwyżej 15 bitów) i jej bity dodatkowe (najwyżej 5) jednym zapisem
		w.bits(uint64(lit.word[ls])|uint64(lv&(1<<le-1))<<lit.length[ls], uint(lit.length[ls])+le)
		d := uint(t&0x7FFF) + 1
		ds, de := distSymbol(int(d))
		w.bits(uint64(dist.word[ds])|uint64((d-1)&(1<<de-1))<<dist.length[ds], uint(dist.length[ds])+de)
	}
	w.bits(uint64(lit.word[endOfBlock]), uint(lit.length[endOfBlock]))
}

// bits dopisuje n ≤ 32 bitów wartości v, od najmłodszego.
func (w *Writer) bits(v uint64, n uint) {
	w.acc |= v << w.nbits
	w.nbits += n
	if w.nbits >= 32 {
		w.out = binary.LittleEndian.AppendUint32(w.out, uint32(w.acc))
		w.acc >>= 32
		w.nbits -= 32
		if len(w.out) >= cap(w.out)-8 {
			w.flush()
		}
	}
}

// flush zapisuje do dst zebrane pełne bajty.
func (w *Writer) flush() {
	w.write(w.out)
	w.out = w.out[:0]
}

func (w *Writer) write(p []byte) {
	if w.err != nil || len(p) == 0 {
		return
	}
	n, err := w.dst.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
}
