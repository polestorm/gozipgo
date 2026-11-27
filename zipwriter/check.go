package zipwriter

import (
	"errors"
	"hash/crc32"
	"io"

	"github.com/polestorm/gozipgo/internal/inflate"
)

// checkReserve to liczba bajtów skompresowanych, które muszą czekać, zanim dekoder zostanie uruchomiony przed
// końcem strumienia. Dekoder ciągnie wejście sam i nie umie czekać na jego dalszy ciąg, więc dostaje pracę tylko
// wtedy, gdy na pewno jej nie przerwie brak danych. Jedno wywołanie Read dekoduje najwyżej okno, czyli 32 768
// bajtów treści: każdy bajt treści kosztuje najwyżej 16 bitów strumienia (dopasowanie trzech bajtów zapisane
// 48 bitami), nagłówek bloku z kodami dynamicznymi poniżej 600 bajtów, a blok kompresora poza ostatnim ma ponad
// 16 000 bajtów treści; do tego 4096 bajtów, które dekoder wczytuje na zapas. Razem poniżej 75 000 bajtów.
const checkReserve = 128 << 10

// errStarved: dekoder poprosił o dane przed końcem strumienia, a kolejka była pusta. Rachunek przy checkReserve
// mówi, że to się nie zdarza; gdyby się zdarzyło, kontrola kończy się niepowodzeniem, nie milczeniem.
var errStarved = errors.New("zipwriter: the self-check ran out of compressed data")

// checker dekoduje strumień DEFLATE wpisu w miarę, jak kompresor go zapisuje, i liczy rozmiar oraz CRC-32 tego,
// co z niego wychodzi. Na końcu wpisu pisarz porównuje je z tym, co dostał od wywołującego: archiwum, którego
// wpis nie rozpakowuje się do swojej treści, nie zostaje ukończone bez błędu (ZW-011).
type checker struct {
	inf  *inflate.Reader
	q    []byte // q[r:] to bajty strumienia, których dekoder jeszcze nie wziął
	r    int
	fed  int64 // bajty strumienia przekazane do kontroli
	last bool  // kompresor skończył: koniec kolejki jest końcem strumienia
	end  bool  // dekoder doszedł do końca strumienia
	err  error
	crc  uint32
	size uint64
	out  []byte
}

// Read podaje dekoderowi bajty z kolejki.
func (c *checker) Read(p []byte) (int, error) {
	if c.r == len(c.q) {
		if c.last {
			return 0, io.EOF
		}
		return 0, errStarved
	}
	n := copy(p, c.q[c.r:])
	c.r += n
	return n, nil
}

// reset zaczyna kontrolę nowego strumienia.
func (c *checker) reset() {
	if c.inf == nil {
		c.inf = inflate.NewReader(c)
		c.out = make([]byte, 32<<10)
	} else {
		c.inf.Reset(c)
	}
	c.q, c.r, c.fed = c.q[:0], 0, 0
	c.last, c.end, c.err = false, false, nil
	c.crc, c.size = 0, 0
}

// feed przyjmuje kolejne bajty strumienia i dekoduje tyle, ile można bez ryzyka braku danych.
func (c *checker) feed(p []byte) {
	if c.r > 0 {
		c.q = c.q[:copy(c.q, c.q[c.r:])]
		c.r = 0
	}
	c.q = append(c.q, p...)
	c.fed += int64(len(p))
	for len(c.q)-c.r >= checkReserve && c.err == nil && !c.end {
		c.step()
	}
}

// step to jedno wywołanie Read dekodera.
func (c *checker) step() {
	n, err := c.inf.Read(c.out)
	c.crc = crc32.Update(c.crc, crc32.IEEETable, c.out[:n])
	c.size += uint64(n)
	switch {
	case err == io.EOF:
		c.end = true
	case err != nil:
		c.err = err
	}
}

// finish dekoduje resztę strumienia i mówi, czy jest on dokładnie strumieniem treści o podanym rozmiarze
// i CRC-32: poprawny, bez bajtów po końcu i bez różnicy w treści.
func (c *checker) finish(crc uint32, size uint64) bool {
	c.last = true
	for c.err == nil && !c.end {
		c.step()
	}
	return c.err == nil && c.inf.Consumed() == c.fed && c.size == size && c.crc == crc
}
