// Package deflate koduje strumień DEFLATE według RFC 1951. Napisany z tekstu RFC (format bloków w rozdziale 3,
// odroczony wybór dopasowań z rozdziału 4) i z opisu algorytmu package-merge (Larmore, Hirschberg, 1990) dla
// kodów o ograniczonej długości. Dopasowań szuka w dwóch tablicach skrótów, bez łańcuchów (ADR 0007);
// compress/flate służy tylko jako wyrocznia w testach (docs/provenance.md).
//
// Algorytm i jego parametry są stałe: te same dane dają te same bajty na każdym systemie, w każdej architekturze
// i w każdej wersji Go, niezależnie od tego, jak wywołujący dzieli dane na wywołania Write. Zmiana którejkolwiek
// stałej z tego pliku, poza rozmiarem bufora, zmienia bajty wyjścia, więc jest zmianą formatu wyniku biblioteki
// (ADR 0005 pkt 2).
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
	minMatch   = 3     // najkrótsze dopasowanie formatu; od niego liczona jest długość w symbolu
	maxMatch   = 258

	// Bufor wejścia: okno, bieżący blok i dane do przodu. Im większy, tym rzadziej dane i tablice trzeba
	// przesuwać; rozmiar nie wpływa na bajty wyjścia.
	bufSize = 32 * windowSize

	// Dwie tablice skrótów bez łańcuchów: krótka pamięta ostatnią pozycję o tych samych czterech bajtach, długa
	// o tych samych ośmiu. Każda pozycja ma więc najwyżej dwóch kandydatów: bliskiego i takiego, który
	// prawdopodobnie zgadza się dalej.
	shortLen  = 4
	longLen   = 8
	shortBits = 15
	longBits  = 17
	shortMul  = 0x9E3779B1         // 2^32 / złoty podział
	longMul   = 0x9E3779B185EBCA87 // 2^64 / złoty podział

	// Blok kończy się, gdy zbliża się do maxTokens symboli albo maxBlockBytes bajtów wejścia, zawsze w chwili,
	// w której żadne dopasowanie nie jest odroczone. Zapas pokrywa to, co może dojść do następnego sprawdzenia:
	// ciąg odroczeń (coraz dłuższe dopasowania, krótsze niż lazyLength, każde z jednym literałem), wydane
	// dopasowanie albo literały bez szukania, a na końcu strumienia jeszcze ostatnie bajty. Dzięki temu blok
	// mieści się w jednym bloku niezakodowanym, a jego dane są jeszcze w buforze.
	maxTokens     = 16384
	maxBlockBytes = 65535
	tokenSlack    = 2*lazyLength + maxSkip
	byteSlack     = 2*maxMatch + 2*lazyLength

	// Pozycja jest kodowana, gdy ma przed sobą tyle bajtów (albo strumień się kończy): najdłuższe dopasowanie
	// i osiem bajtów skrótu ostatniej pozycji, którą ono obejmuje.
	lookahead = maxMatch + longLen

	lazyLength = 32   // od tej długości dopasowanie jest wydawane bez sprawdzenia następnej pozycji
	goodLength = 8    // gdy kandydat z tablicy długiej daje co najmniej tyle, bliskiego nie sprawdzamy
	farLimit   = 2048 // dopasowanie najkrótsze (cztery bajty) dalej niż tyle kosztuje więcej niż literały
	tailInsert = 64   // z pozycji objętych dopasowaniem do tablic trafia tylko tyle ostatnich

	// Dane bez dopasowań: po każdych 2^missShift kolejnych pozycjach bez trafienia o jedną pozycję więcej idzie
	// literałem bez szukania, najwyżej maxSkip naraz. Pierwsze trafienie zeruje licznik.
	missShift = 5
	maxSkip   = 32

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
	start      int // początek danych bieżącego strumienia; wcześniejsze pozycje nie są kandydatami
	pos        int // następna pozycja do zakodowania
	blockStart int // początek danych bieżącego bloku

	// head[skrót czterech bajtów] i headL[skrót ośmiu] trzymają pozycje w buf powiększone o 1; 0 to brak.
	head  []int32
	headL []int32

	// Odroczone dopasowanie znalezione na pozycji pos−1; pendLen = 0, gdy go nie ma.
	pendLen, pendDist int
	misses            int // kolejne pozycje bez dopasowania

	// Symbole bieżącego bloku. Literał: bajt; dopasowanie: 1<<31 | symbol odległości<<24 | (długość−3)<<16 |
	// (odległość−1). Częstości symboli są liczone na bieżąco.
	tokens   []uint32
	nt       int
	litFreq  [numLitLen]int32
	distFreq [numDist]int32

	lit, dist, cl code

	// Długości kodów bloku dynamicznego zakodowane symbolami 0–18; bity dodatkowe w starszym bajcie.
	clSyms [numLitLen + numDist]uint16
	clFreq [numCodeLen]int32

	acc   uint64 // bity czekające na zapis, od najmłodszego
	nbits uint
	out   []byte // out[:o] to pełne bajty czekające na zapis do dst
	o     int
}

const outSize = 1 << 15

// NewWriter zwraca koder strumienia zapisywanego do dst.
func NewWriter(dst io.Writer) *Writer {
	return &Writer{
		dst:    dst,
		buf:    make([]byte, bufSize),
		head:   make([]int32, 1<<shortBits),
		headL:  make([]int32, 1<<longBits),
		tokens: make([]uint32, maxTokens),
		out:    make([]byte, outSize),
	}
}

// Reset przygotowuje koder do nowego strumienia zapisywanego do dst, bez nowej alokacji. Wynik nie zależy od
// tego, co koder kodował wcześniej: tablice skrótów nie są czyszczone, ale nowy strumień zaczyna się w buforze
// za danymi poprzedniego, a pozycja sprzed początku strumienia nie jest kandydatem, tak samo jak brak pozycji.
func (w *Writer) Reset(dst io.Writer) {
	w.dst, w.err = dst, nil
	w.start, w.pos, w.blockStart = w.n, w.n, w.n
	w.pendLen, w.pendDist, w.misses = 0, 0, 0
	w.nt = 0
	w.litFreq, w.distFreq = [numLitLen]int32{}, [numDist]int32{}
	w.acc, w.nbits, w.o = 0, 0, 0
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
	w.block(true)
	if w.nbits > 0 {
		w.nbits = (w.nbits + 7) &^ 7 // dopełnienie ostatniego bajtu zerami
		w.spill()
	}
	w.flush()
	return w.err
}

// slide przesuwa dane na początek bufora, zostawiając okno przed bieżącą pozycją i dane bieżącego bloku,
// i przelicza pozycje w tablicach. Wywoływane przy pełnym buforze po compress(false), więc jest o co przesunąć.
func (w *Writer) slide() {
	shift := min(w.blockStart, max(w.pos-windowSize, w.start))
	if shift <= 0 {
		return
	}
	copy(w.buf, w.buf[shift:w.n])
	w.n -= shift
	w.pos -= shift
	w.blockStart -= shift
	w.start = max(w.start-shift, 0)
	s := int32(shift)
	for i, v := range w.head {
		w.head[i] = max(v-s, 0)
	}
	for i, v := range w.headL {
		w.headL[i] = max(v-s, 0)
	}
}

func load32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i:]) }
func load64(b []byte, i int) uint64 { return binary.LittleEndian.Uint64(b[i:]) }

// extend zwraca, ile bajtów od pozycji a i od pozycji b jest takich samych, licząc od l i nie dalej niż do limit.
func extend(buf []byte, a, b, l, limit int) int {
	for l+8 <= limit {
		if x := load64(buf, a+l) ^ load64(buf, b+l); x != 0 {
			return l + bits.TrailingZeros64(x)>>3
		}
		l += 8
	}
	for l < limit && buf[a+l] == buf[b+l] {
		l++
	}
	return l
}

// match dopisuje do częstości symbole dopasowania i zwraca jego zapis w tokens.
func (w *Writer) match(length, dist int) uint32 {
	ds, _ := distSymbol(dist)
	w.litFreq[257+int(lengthSymbol[length-minMatch])]++
	w.distFreq[ds]++
	return 1<<31 | uint32(ds)<<24 | uint32(length-minMatch)<<16 | uint32(dist-1)
}

// compress koduje pozycje, dla których w buforze jest pełny zapas danych do przodu; z final=true wszystkie.
// Decyzja o każdej pozycji zależy tylko od danych strumienia, nie od chwili wywołania ani od podziału na
// wywołania Write.
//
// Jeden obrót pętli to jedna pozycja p: osiem bajtów od p daje oba skróty, tablice podają dwóch kandydatów
// i od razu zapamiętują p. Dopasowanie krótsze niż lazyLength czeka jeden obrót (pendLen): jeśli pozycja p+1 ma
// dłuższe, bajt p idzie literałem (RFC 1951, rozdz. 4). Stan pętli jest w zmiennych lokalnych i wraca do w przed
// wywołaniem block (pozycja i liczba symboli) i na końcu.
func (w *Writer) compress(final bool) {
	buf := w.buf[:w.n]
	n := len(buf)
	// Tablice jako wskaźniki na tablice o znanym rozmiarze: skrót mieści się w nich z definicji.
	head, headL := (*[1 << shortBits]int32)(w.head), (*[1 << longBits]int32)(w.headL)
	tokens, litFreq := w.tokens, &w.litFreq
	p, nt, misses := w.pos, w.nt, w.misses
	pendLen, pendDist := w.pendLen, w.pendDist
	start := w.start
	end := n - lookahead // ostatnia pozycja z pełnym zapasem
	if final {
		end = n - longLen // ostatnia, od której da się wczytać osiem bajtów
	}
	for p <= end {
		if pendLen == 0 && (nt > maxTokens-tokenSlack || p-w.blockStart > maxBlockBytes-byteSlack) {
			w.pos, w.nt = p, nt
			w.block(false)
			nt = 0
			if w.err != nil {
				break
			}
		}
		x := load64(buf, p)
		hs := uint32(x) * shortMul >> (32 - shortBits)
		hl := x * longMul >> (64 - longBits)
		cs, cl := int(head[hs])-1, int(headL[hl])-1
		head[hs], headL[hl] = int32(p+1), int32(p+1)

		// Kandydat musi leżeć w oknie i w bieżącym strumieniu; brak kandydata to pozycja −1.
		lowest := max(p-windowSize, start)
		limit := min(n-p, maxMatch)
		length, dist := pendLen, 0
		if cl >= lowest && load32(buf, cl) == uint32(x) {
			if l := extend(buf, cl, p, shortLen, limit); l > length {
				length, dist = l, p-cl
			}
		}
		if length < goodLength && cs >= lowest && cs != cl && load32(buf, cs) == uint32(x) {
			// przy równej długości wygrywa kandydat bliższy: jego odległość ma krótszy zapis
			if l := extend(buf, cs, p, shortLen, limit); l > length || l == length && dist > p-cs {
				length, dist = l, p-cs
			}
		}
		if length == shortLen && dist > farLimit {
			dist = 0
		}

		from := p // początek dopasowania do wydania
		if dist == 0 {
			if pendLen == 0 {
				// Nic tu nie pasuje: literał, a po dłuższej serii takich pozycji kilka następnych bez szukania.
				misses++
				skip := min(misses>>missShift, maxSkip, n-p-1)
				for stop := p + 1 + skip; p < stop; p++ {
					tokens[nt] = uint32(buf[p])
					litFreq[buf[p]]++
					nt++
				}
				continue
			}
			// Pozycja p nie ma nic dłuższego: wydajemy dopasowanie odroczone, które zaczyna się na p−1.
			from, length, dist = p-1, pendLen, pendDist
		} else {
			if pendLen > 0 {
				tokens[nt] = uint32(buf[p-1])
				litFreq[buf[p-1]]++
				nt++
			}
			if length < lazyLength {
				pendLen, pendDist = length, dist
				misses = 0
				p++
				continue
			}
		}
		// Dopasowanie rozszerzamy wstecz o literały, które stoją tuż przed nim w tym bloku i zgadzają się
		// z bajtami przed kandydatem: tablice pamiętają tylko ostatnią pozycję, więc początek powtórzenia
		// bywa znaleziony o kilka bajtów za późno. Odległość się nie zmienia, koniec dopasowania też nie.
		for nt > 0 && tokens[nt-1] < 1<<31 && length < maxMatch && from-dist > start && buf[from-1] == buf[from-dist-1] {
			nt--
			litFreq[tokens[nt]]--
			from--
			length++
		}
		tokens[nt] = w.match(length, dist)
		nt++
		pendLen, misses = 0, 0
		stop := from + length
		// Ostatnie pozycje objęte dopasowaniem trafiają do tablic bez szukania; p już tam jest. W długim
		// powtórzeniu wcześniejsze pozycje niczego nie wnoszą, a kosztowałyby najwięcej.
		for q := max(p+1, stop-tailInsert); q < stop && q+longLen <= n; q++ {
			y := load64(buf, q)
			head[uint32(y)*shortMul>>(32-shortBits)] = int32(q + 1)
			headL[y*longMul>>(64-longBits)] = int32(q + 1)
		}
		p = stop
	}
	if final && w.err == nil {
		if pendLen > 0 {
			tokens[nt] = w.match(pendLen, pendDist)
			nt++
			p += pendLen - 1
			pendLen = 0
		}
		for ; p < n; p++ {
			tokens[nt] = uint32(buf[p])
			litFreq[buf[p]]++
			nt++
		}
	}
	w.pos, w.nt, w.misses, w.pendLen, w.pendDist = p, nt, misses, pendLen, pendDist
}

// block zapisuje symbole bieżącego bloku w najtańszej z trzech postaci (RFC 1951, 3.2.3): niezakodowanej, ze
// stałymi kodami albo z kodami dynamicznymi. Przy równym koszcie wygrywa postać wcześniejsza na tej liście.
// Blok obejmuje dane do bieżącej pozycji; wywoływane tylko wtedy, gdy żadne dopasowanie nie jest odroczone.
func (w *Writer) block(final bool) {
	if w.err != nil {
		return
	}
	blockLen := w.pos - w.blockStart
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
	stored := int64((8-(w.nbits+3)%8)%8) + 32 + 8*int64(blockLen)

	last := uint64(0)
	if final {
		last = 1
	}
	switch {
	case blockLen <= maxBlockBytes && stored <= fixed && stored <= dynamic: // pole długości ma 16 bitów
		w.bits(last, 3) // BTYPE 00
		if pad := (8 - w.nbits%8) % 8; pad > 0 {
			w.bits(0, pad)
		}
		w.bits(uint64(blockLen), 16)
		w.bits(uint64(^uint16(blockLen)), 16)
		w.spill() // liczba bitów jest wielokrotnością ośmiu, więc nie zostaje żaden
		w.flush()
		w.write(w.buf[w.blockStart : w.blockStart+blockLen])
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
	w.blockStart += blockLen
	w.nt = 0
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

// symbols zapisuje symbole bloku podanymi kodami i znak końca bloku. Stan zapisu bitów jest w zmiennych
// lokalnych; pełne bajty schodzą do out po osiem naraz, tak jak w spill.
func (w *Writer) symbols(lit, dist *code) {
	acc, nb, out, o := w.acc, w.nbits, w.out, w.o
	for _, t := range w.tokens[:w.nt] {
		// Przed symbolem zostaje najwyżej 7 bitów (dopasowanie, do 48 bitów) albo najwyżej 48 (literał, do 15).
		if nb > 48 || t >= 1<<31 {
			binary.LittleEndian.PutUint64(out[o:], acc)
			k := nb >> 3
			o += int(k)
			acc >>= k * 8
			nb &= 7
			if o > len(out)-8 {
				w.write(out[:o])
				o = 0
			}
		}
		if t < 1<<31 {
			acc |= uint64(lit.word[t]) << nb
			nb += uint(lit.length[t])
			continue
		}
		lv := t >> 16 & 0xFF
		ls := 257 + uint(lengthSymbol[lv])
		le := uint(lengthExtra[lv])
		acc |= (uint64(lit.word[ls]) | uint64(lv&(1<<le-1))<<lit.length[ls]) << nb
		nb += uint(lit.length[ls]) + le
		ds := uint(t >> 24 & 0x1F)
		de := uint(0)
		if ds > 3 {
			de = ds/2 - 1
		}
		acc |= (uint64(dist.word[ds]) | uint64(t&(1<<de-1))<<dist.length[ds]) << nb
		nb += uint(dist.length[ds]) + de
	}
	w.acc, w.nbits, w.o = acc, nb, o
	w.spill()
	w.bits(uint64(lit.word[endOfBlock]), uint(lit.length[endOfBlock]))
}

// bits dopisuje n ≤ 32 bitów wartości v, od najmłodszego.
func (w *Writer) bits(v uint64, n uint) {
	w.acc |= v << w.nbits
	w.nbits += n
	if w.nbits >= 32 {
		w.spill()
	}
}

// spill przenosi pełne bajty z bufora bitów do out; zostaje mniej niż osiem bitów.
func (w *Writer) spill() {
	binary.LittleEndian.PutUint64(w.out[w.o:], w.acc)
	k := w.nbits >> 3
	w.o += int(k)
	w.acc >>= k * 8
	w.nbits &= 7
	if w.o > len(w.out)-8 {
		w.flush()
	}
}

// flush zapisuje do dst zebrane pełne bajty.
func (w *Writer) flush() {
	w.write(w.out[:w.o])
	w.o = 0
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
