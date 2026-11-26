// Package inflate dekoduje strumień DEFLATE według RFC 1951. Napisany z tekstu RFC; compress/flate służy tylko
// jako wyrocznia w testach (docs/provenance.md).
//
// Dekoder ma stałą pamięć (okno 32 KiB, bufor wejścia, tablice kodów w strukturze Reader), nie alokuje w czasie
// dekodowania i nie panikuje dla żadnego wejścia. Każda wada strumienia to *Error ze stałym rodzajem.
package inflate

import (
	"encoding/binary"
	"io"
	"math/bits"
)

// Kind to rodzaj wady strumienia; pakiet zipreader zamienia go na stały kod błędu.
type Kind int

const (
	// BlockType: zarezerwowany typ bloku, BTYPE 11 (RFC 1951, 3.2.3).
	BlockType Kind = iota + 1
	// StoredLength: w bloku niezakodowanym NLEN nie jest dopełnieniem LEN (3.2.4).
	StoredLength
	// CodeLengths: opis kodów dynamicznych nie wyznacza poprawnych kodów Huffmana (3.2.2, 3.2.7).
	CodeLengths
	// Distance: symbol długości albo odległości spoza alfabetu, kod nieprzypisany żadnemu symbolowi albo
	// odległość sięgająca przed początek wyjścia (3.2.3, 3.2.5, 3.2.6).
	Distance
	// Truncated: wejście kończy się przed końcem ostatniego bloku.
	Truncated
)

// Error to wada strumienia DEFLATE. Offset to liczba bajtów wejścia pobranych do chwili wykrycia.
type Error struct {
	Kind   Kind
	Offset int64
}

func (e *Error) Error() string {
	switch e.Kind {
	case BlockType:
		return "inflate: reserved block type"
	case StoredLength:
		return "inflate: the length of a stored block does not match its complement"
	case CodeLengths:
		return "inflate: the code lengths do not describe a Huffman code"
	case Distance:
		return "inflate: an invalid length or distance"
	case Truncated:
		return "inflate: the stream ends before its last block does"
	}
	return "inflate: invalid stream"
}

const (
	windowSize  = 1 << 15 // największa odległość odwołania (3.2.5)
	maxCodeBits = 15      // najdłuższy kod alfabetu znaków i odległości (3.2.7)
	primaryBits = 9       // tyle bitów indeksuje tablicę szybkiego dekodowania
	maxLitCodes = 288     // kody stałe obejmują symbole 0–287 (3.2.6)
	maxDist     = 32      // pole HDIST mieści 1–32 (3.2.7)
	endOfBlock  = 256
)

// Długości i odległości bazowe oraz liczby bitów dodatkowych: tabele z RFC 1951, 3.2.5.
var (
	lengthBase  = [29]uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lengthExtra = [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distBase    = [30]uint16{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distExtra   = [30]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	// Kolejność, w jakiej zapisane są długości kodów alfabetu długości kodów (3.2.7).
	codeLengthOrder = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
)

// huffman to jeden kod Huffmana w postaci kanonicznej z RFC 1951, 3.2.2: kody tej samej długości są kolejnymi
// liczbami w porządku symboli, a krótsze poprzedzają dłuższe.
type huffman struct {
	// table[k] dla k = najbliższych primaryBits bitów wejścia (bit najwcześniejszy najniżej): symbol<<4 | długość
	// kodu, gdy kod ma najwyżej primaryBits bitów; zero, gdy jest dłuższy albo nieprzypisany.
	table [1 << primaryBits]uint16
	// count[l] to liczba kodów długości l, first[l] najmniejszy kod tej długości (next_code z RFC), offset[l]
	// indeks pierwszego symbolu tej długości w symbols.
	count   [maxCodeBits + 1]uint16
	first   [maxCodeBits + 1]uint16
	offset  [maxCodeBits + 1]uint16
	symbols [maxLitCodes]uint16
}

// Wynik budowania kodu.
const (
	codeComplete = iota // każdy ciąg bitów zaczyna się dokładnie jednym kodem
	codeIncomplete
	codeOversubscribed
	codeEmpty // żaden symbol nie ma kodu
)

// build wyznacza kod z długości kodów symboli (algorytm z RFC 1951, 3.2.2) i mówi, czy jest zupełny.
func (h *huffman) build(lengths []uint8) int {
	h.count = [maxCodeBits + 1]uint16{}
	for _, l := range lengths {
		h.count[l]++
	}
	h.count[0] = 0
	// Suma Krafta: zestaw długości jest zupełny, gdy kody wypełniają całą przestrzeń 2^15.
	space, used := 1, 0
	for l := 1; l <= maxCodeBits; l++ {
		space = space<<1 - int(h.count[l])
		used += int(h.count[l])
		if space < 0 {
			return codeOversubscribed
		}
	}
	h.table = [1 << primaryBits]uint16{}
	if used == 0 {
		return codeEmpty
	}
	var next [maxCodeBits + 1]uint16
	code, index := uint16(0), uint16(0)
	for l := 1; l <= maxCodeBits; l++ {
		code = (code + h.count[l-1]) << 1
		h.first[l], next[l] = code, index
		h.offset[l] = index
		index += h.count[l]
	}
	for s, l := range lengths {
		if l == 0 {
			continue
		}
		pos := next[l]
		next[l]++
		h.symbols[pos] = uint16(s)
		if l <= primaryBits {
			// Kod jest zapisany w strumieniu od bitu najbardziej znaczącego (3.1.1), a bity czytamy od
			// najniższego, więc indeksem tablicy jest kod odwrócony.
			c := h.first[l] + (pos - h.offset[l])
			rev := bits.Reverse16(c) >> (16 - l)
			for k := int(rev); k < 1<<primaryBits; k += 1 << l {
				h.table[k] = uint16(s)<<4 | uint16(l)
			}
		}
	}
	if space != 0 {
		return codeIncomplete
	}
	return codeComplete
}

// Kody stałe z RFC 1951, 3.2.6.
var fixedLit, fixedDist = func() (lit, dist huffman) {
	var l [maxLitCodes]uint8
	for s := range l {
		switch {
		case s < 144:
			l[s] = 8
		case s < 256:
			l[s] = 9
		case s < 280:
			l[s] = 7
		default:
			l[s] = 8
		}
	}
	lit.build(l[:])
	var d [maxDist]uint8
	for s := range d {
		d[s] = 5
	}
	dist.build(d[:])
	return
}()

// Stany dekodera między wywołaniami decode.
const (
	stateHeader  = iota // przed nagłówkiem bloku
	stateStored         // w danych bloku niezakodowanego
	stateSymbols        // w danych bloku z kodami
	stateCopy           // w trakcie kopiowania odwołania, które nie zmieściło się w oknie
)

// Reader dekoduje jeden strumień DEFLATE czytany z src.
type Reader struct {
	src    io.Reader
	srcErr error // pierwszy błąd src, także io.EOF
	in     [4096]byte
	ip, ie int
	pulled int64 // bajty wejścia przeniesione do bufora bitów

	bb uint64 // bufor bitów: najwcześniejszy bit najniżej
	nb uint   // liczba ważnych bitów w bb

	win     [windowSize]byte
	wp, rp  int   // win[rp:wp] czeka na wydanie
	flushed int64 // bajty wyjścia sprzed bieżącego obrotu okna

	state      int
	final      bool
	lit, dist  *huffman
	distEmpty  bool // blok nie ma żadnego kodu odległości
	dynLit     huffman
	dynDist    huffman
	codeLen    huffman
	storedLeft int
	copyLen    int
	copyDist   int
	err        error
}

// NewReader zwraca dekoder strumienia czytanego z src.
func NewReader(src io.Reader) *Reader { return &Reader{src: src} }

// Consumed zwraca liczbę bajtów wejścia zużytych przez strumień. Po końcu strumienia (Read zwróciło io.EOF) to
// długość strumienia w bajtach: bity dopełniające ostatni bajt należą do niego.
func (r *Reader) Consumed() int64 { return r.pulled - int64(r.nb/8) }

// Read wydaje kolejne bajty wyjścia. Bajty zdekodowane przed wadą strumienia są wydawane przed błędem, więc
// to, ile bajtów poprzedza błąd, nie zależy od rozmiaru p.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.rp < r.wp {
			n := copy(p, r.win[r.rp:r.wp])
			r.rp += n
			return n, nil
		}
		if r.err != nil {
			return 0, r.err
		}
		if r.wp == windowSize {
			r.flushed += windowSize
			r.wp, r.rp = 0, 0
		}
		r.decode()
	}
}

// more wczytuje kolejną porcję wejścia; false, gdy wejście się skończyło albo zwróciło błąd.
func (r *Reader) more() bool {
	for tries := 0; r.srcErr == nil && tries < 16; tries++ {
		n, err := r.src.Read(r.in[:])
		if n < 0 || n > len(r.in) {
			n, err = 0, io.ErrNoProgress
		}
		if err != nil {
			r.srcErr = err
		}
		if n > 0 {
			r.ip, r.ie = 0, n
			return true
		}
	}
	if r.srcErr == nil {
		r.srcErr = io.ErrNoProgress
	}
	return false
}

// fill dopełnia bufor bitów do ponad 56 bitów, o ile wejście na to pozwala.
func (r *Reader) fill() {
	if r.ie-r.ip >= 8 {
		// Osiem bajtów naraz: bity ponad nb są już wartościami bajtów, które dopiero zostaną policzone, więc
		// ponowne ich nałożenie niczego nie zmienia.
		r.bb |= binary.LittleEndian.Uint64(r.in[r.ip:]) << r.nb
		n := (63 - r.nb) >> 3
		r.ip += int(n)
		r.nb += n << 3
		r.pulled += int64(n)
		return
	}
	for r.nb <= 56 {
		if r.ip == r.ie && !r.more() {
			return
		}
		r.bb |= uint64(r.in[r.ip]) << r.nb
		r.ip++
		r.nb += 8
		r.pulled++
	}
}

// short ustawia błąd braku wejścia: błąd źródła bez zmiany, a jego koniec jako Truncated.
func (r *Reader) short() {
	if r.srcErr != nil && r.srcErr != io.EOF {
		r.err = r.srcErr
		return
	}
	r.fail(Truncated)
}

func (r *Reader) fail(k Kind) { r.err = &Error{Kind: k, Offset: r.Consumed()} }

// need zapewnia n bitów (n ≤ 32) w buforze; false i ustawiony błąd, gdy wejście się skończyło.
func (r *Reader) need(n uint) bool {
	if r.nb < n {
		r.fill()
		if r.nb < n {
			r.short()
			return false
		}
	}
	return true
}

// take zdejmuje n bitów, o których wiadomo, że są w buforze.
func (r *Reader) take(n uint) uint32 {
	v := uint32(r.bb) & (1<<n - 1)
	r.bb >>= n
	r.nb -= n
	return v
}

// symbol dekoduje jeden symbol kodu h; wywołujący zadbał o fill. Zwraca −1 po ustawieniu błędu: bad to rodzaj
// błędu dla ciągu bitów, któremu kod nie przypisuje symbolu (możliwe tylko w kodzie niezupełnym).
func (r *Reader) symbol(h *huffman, bad Kind) int {
	if e := h.table[r.bb&(1<<primaryBits-1)]; e != 0 {
		l := uint(e & 15)
		if l > r.nb {
			r.short()
			return -1
		}
		r.bb >>= l
		r.nb -= l
		return int(e >> 4)
	}
	// Kod dłuższy niż primaryBits albo nieprzypisany: bit po bicie, od najbardziej znaczącego bitu kodu.
	code := uint16(0)
	for l := uint(1); l <= maxCodeBits; l++ {
		if l > r.nb {
			r.short()
			return -1
		}
		code = code<<1 | uint16(r.bb>>(l-1))&1
		if d := code - h.first[l]; code >= h.first[l] && d < h.count[l] {
			r.bb >>= l
			r.nb -= l
			return int(h.symbols[h.offset[l]+d])
		}
	}
	r.fail(bad)
	return -1
}

// decode posuwa dekodowanie, aż okno się zapełni, strumień się skończy albo wystąpi błąd.
func (r *Reader) decode() {
	for r.wp < windowSize && r.err == nil {
		switch r.state {
		case stateHeader:
			r.header()
		case stateStored:
			r.stored()
		case stateCopy:
			r.copyMatch()
		default:
			r.symbols()
		}
	}
}

// header czyta nagłówek bloku (3.2.3) i, dla kodów dynamicznych, ich opis.
func (r *Reader) header() {
	if r.final {
		r.err = io.EOF
		return
	}
	if !r.need(3) {
		return
	}
	r.final = r.take(1) == 1
	switch r.take(2) {
	case 0:
		// Bity do granicy bajtu są pomijane (3.2.4), potem LEN i NLEN.
		r.take(r.nb & 7)
		if !r.need(32) {
			return
		}
		n := r.take(16)
		if r.take(16) != n^0xFFFF {
			r.fail(StoredLength)
			return
		}
		r.storedLeft, r.state = int(n), stateStored
	case 1:
		r.lit, r.dist, r.distEmpty, r.state = &fixedLit, &fixedDist, false, stateSymbols
	case 2:
		if r.dynamic() {
			r.lit, r.dist, r.state = &r.dynLit, &r.dynDist, stateSymbols
		}
	default:
		r.fail(BlockType)
	}
}

// dynamic czyta opis kodów dynamicznych (3.2.7).
func (r *Reader) dynamic() bool {
	if !r.need(14) {
		return false
	}
	nlit, ndist, nclen := int(r.take(5))+257, int(r.take(5))+1, int(r.take(4))+4
	// 3.2.7 podaje dla HLIT zakres 257–286; symbole odległości 30 i 31 nie występują w danych (3.2.6), więc
	// opis z więcej niż 30 kodami odległości nie pochodzi z żadnego kodera i też jest odrzucany.
	if nlit > 286 || ndist > 30 {
		r.fail(CodeLengths)
		return false
	}
	var cl [19]uint8
	for i := 0; i < nclen; i++ {
		if !r.need(3) {
			return false
		}
		cl[codeLengthOrder[i]] = uint8(r.take(3))
	}
	if r.codeLen.build(cl[:]) != codeComplete {
		r.fail(CodeLengths)
		return false
	}
	// Długości obu alfabetów tworzą jeden ciąg: powtórzenie może przejść z jednego w drugi.
	var lengths [maxLitCodes + maxDist]uint8
	total := nlit + ndist
	for i := 0; i < total; {
		r.fill()
		s := r.symbol(&r.codeLen, CodeLengths)
		if s < 0 {
			return false
		}
		if s < 16 {
			lengths[i] = uint8(s)
			i++
			continue
		}
		var rep int
		var value uint8
		switch s {
		case 16:
			if i == 0 {
				r.fail(CodeLengths) // nie ma poprzedniej długości do powtórzenia
				return false
			}
			if !r.need(2) {
				return false
			}
			rep, value = 3+int(r.take(2)), lengths[i-1]
		case 17:
			if !r.need(3) {
				return false
			}
			rep = 3 + int(r.take(3))
		default:
			if !r.need(7) {
				return false
			}
			rep = 11 + int(r.take(7))
		}
		if rep > total-i {
			r.fail(CodeLengths)
			return false
		}
		for ; rep > 0; rep-- {
			lengths[i] = value
			i++
		}
	}
	// Bez kodu końca bloku blok nie mógłby się skończyć.
	if lengths[endOfBlock] == 0 || r.dynLit.build(lengths[:nlit]) != codeComplete {
		r.fail(CodeLengths)
		return false
	}
	// Kod odległości: zupełny, pusty (blok samych znaków) albo jeden kod o długości 1 (3.2.7).
	r.distEmpty = false
	switch r.dynDist.build(lengths[nlit:total]) {
	case codeComplete:
	case codeEmpty:
		r.distEmpty = true
	case codeIncomplete:
		if r.dynDist.count[1] != 1 || ndistUsed(lengths[nlit:total]) != 1 {
			r.fail(CodeLengths)
			return false
		}
	default:
		r.fail(CodeLengths)
		return false
	}
	return true
}

// ndistUsed liczy symbole, które mają kod.
func ndistUsed(lengths []uint8) int {
	n := 0
	for _, l := range lengths {
		if l != 0 {
			n++
		}
	}
	return n
}

// stored kopiuje dane bloku niezakodowanego.
func (r *Reader) stored() {
	for r.storedLeft > 0 && r.wp < windowSize {
		if r.nb >= 8 {
			// Bajty, które już trafiły do bufora bitów (po nagłówku bufor stoi na granicy bajtu).
			r.win[r.wp] = byte(r.take(8))
			r.wp++
			r.storedLeft--
			continue
		}
		r.bb, r.nb = 0, 0
		if r.ip == r.ie && !r.more() {
			r.short()
			return
		}
		n := min(r.storedLeft, windowSize-r.wp, r.ie-r.ip)
		copy(r.win[r.wp:], r.in[r.ip:r.ip+n])
		r.wp += n
		r.ip += n
		r.pulled += int64(n)
		r.storedLeft -= n
	}
	if r.storedLeft == 0 {
		r.state = stateHeader
	}
}

// symbols dekoduje znaki i odwołania bloku z kodami (3.2.3, 3.2.5).
func (r *Reader) symbols() {
	for r.wp < windowSize {
		// Najdłuższy ciąg na jeden krok: 15 + 5 bitów długości i 15 + 13 bitów odległości, razem 48.
		r.fill()
		s := r.symbol(r.lit, CodeLengths)
		if s < endOfBlock {
			if s < 0 {
				return
			}
			r.win[r.wp] = byte(s)
			r.wp++
			continue
		}
		if s == endOfBlock {
			r.state = stateHeader
			return
		}
		s -= 257
		if s >= len(lengthBase) {
			r.fail(Distance) // symbole 286 i 287 nie występują w danych (3.2.6)
			return
		}
		x := uint(lengthExtra[s])
		if x > r.nb {
			r.short()
			return
		}
		length := int(lengthBase[s]) + int(r.take(x))
		if r.distEmpty {
			r.fail(Distance)
			return
		}
		d := r.symbol(r.dist, Distance)
		if d < 0 {
			return
		}
		if d >= len(distBase) {
			r.fail(Distance) // symbole 30 i 31 nie występują w danych (3.2.6)
			return
		}
		x = uint(distExtra[d])
		if x > r.nb {
			r.short()
			return
		}
		dist := int(distBase[d]) + int(r.take(x))
		// Odległość nie może sięgać przed początek wyjścia (3.2.3).
		if int64(dist) > r.flushed+int64(r.wp) {
			r.fail(Distance)
			return
		}
		r.copyLen, r.copyDist = length, dist
		r.copyMatch()
		if r.state == stateCopy {
			return
		}
	}
}

// copyMatch kopiuje odwołanie w oknie; gdy okno się zapełni, zostawia resztę na następne wywołanie.
func (r *Reader) copyMatch() {
	n := min(r.copyLen, windowSize-r.wp)
	r.copyLen -= n
	sp := r.wp - r.copyDist
	if sp >= 0 && r.copyDist >= n {
		copy(r.win[r.wp:r.wp+n], r.win[sp:sp+n])
		r.wp += n
	} else {
		// Źródło zachodzi na cel (odległość mniejsza niż długość) albo leży w poprzednim obrocie okna.
		if sp < 0 {
			sp += windowSize
		}
		for ; n > 0; n-- {
			r.win[r.wp] = r.win[sp]
			r.wp++
			sp = (sp + 1) & (windowSize - 1)
		}
	}
	if r.copyLen > 0 {
		r.state = stateCopy
	} else {
		r.state = stateSymbols
	}
}
