package zipwriter

import (
	"errors"
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

// errDiffers: treść odczytana ze strumienia różni się od treści wpisu.
var errDiffers = errors.New("zipwriter: the decoded data differ from the content")

// errStarved: dekoder poprosił o dane przed końcem strumienia, a kolejka była pusta. Rachunek przy checkReserve
// mówi, że to się nie zdarza; gdyby się zdarzyło, kontrola kończy się niepowodzeniem, nie milczeniem.
var errStarved = errors.New("zipwriter: the self-check ran out of compressed data")

// pendKeep to największa kolejka treści, którą kontrola zostawia sobie na następny wpis; większa wraca do
// systemu, żeby jeden wpis o bardzo powtarzalnej treści nie zajmował pamięci do końca życia pisarza.
const pendKeep = 4 << 20

// checker dekoduje strumień DEFLATE wpisu w miarę, jak kompresor go zapisuje, i porównuje to, co z niego
// wychodzi, bajt po bajcie z tym, co pisarz dostał od wywołującego: archiwum, którego wpis nie rozpakowuje się
// do swojej treści, nie zostaje ukończone bez błędu (ZW-011). Wynik nie zależy od żadnej sumy kontrolnej.
//
// Treść do porównania to ref[rp:]. Gdy pisarz ma ją całą (Add), podaje ją przez expect i nic nie jest kopiowane.
// Gdy treść płynie (Create), pisarz podaje ją przez content przed oddaniem kompresorowi, a kontrola trzyma kopię
// do chwili, w której dekoder ją odczyta. Dekoder rusza dopiero przy checkReserve bajtach strumienia, a kompresor
// zbiera do 1 MiB wejścia, więc kolejka ma zwykle 1 do 2 MiB; przy treści kompresowanej bardzo mocno rośnie do
// tego, co mieści checkReserve bajtów strumienia (około 130 MiB przy 1000:1).
type checker struct {
	inf  *inflate.Reader
	q    []byte // q[r:] to bajty strumienia, których dekoder jeszcze nie wziął
	r    int
	fed  int64 // bajty strumienia przekazane do kontroli
	last bool  // kompresor skończył: koniec kolejki jest końcem strumienia
	end  bool  // dekoder doszedł do końca strumienia
	err  error
	out  []byte
	ref  []byte // ref[rp:] to treść wpisu, której dekoder jeszcze nie odczytał
	rp   int
	pend []byte // własna kolejka treści wpisu strumieniowego; ref wskazuje na nią albo na treść z expect
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

// reset zaczyna kontrolę nowego strumienia, z pustą treścią.
func (c *checker) reset() {
	if c.inf == nil {
		c.inf = inflate.NewReader(c)
		c.out = make([]byte, 32<<10)
	} else {
		c.inf.Reset(c)
	}
	c.q, c.r, c.fed = c.q[:0], 0, 0
	c.last, c.end, c.err = false, false, nil
	if cap(c.pend) > pendKeep {
		c.pend = nil
	}
	c.pend = c.pend[:0]
	c.ref, c.rp = c.pend, 0
}

// expect podaje całą treść wpisu. Kontrola jej nie kopiuje i nie zmienia.
func (c *checker) expect(content []byte) {
	c.ref, c.rp = content, 0
}

// content dopisuje kolejny kawałek treści wpisu strumieniowego. Musi być wywołane, zanim kompresor dostanie te
// bajty: dekoder nie odczyta wtedy niczego, czego kontrola jeszcze nie zna. Nie łączy się z expect.
func (c *checker) content(p []byte) {
	if c.rp > len(c.pend)/2 {
		// Odczytana połowa kolejki: przesunięcie kosztuje mniej niż to, co już porównano.
		c.pend = c.pend[:copy(c.pend, c.pend[c.rp:])]
		c.rp = 0
	}
	c.pend = append(c.pend, p...)
	c.ref = c.pend
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

// step to jedno wywołanie Read dekodera i porównanie tego, co odczytał, z kolejnymi bajtami treści.
func (c *checker) step() {
	n, err := c.inf.Read(c.out)
	rest := c.ref[c.rp:]
	if n > len(rest) || string(c.out[:n]) != string(rest[:n]) {
		// Także wtedy, gdy strumień daje więcej, niż treść ma bajtów.
		c.err = errDiffers
		return
	}
	c.rp += n
	switch {
	case err == io.EOF:
		c.end = true
	case err != nil:
		c.err = err
	}
}

// finish dekoduje resztę strumienia i mówi, czy jest on dokładnie strumieniem treści wpisu: poprawny, bez
// bajtów po końcu, a odczytane bajty są wszystkimi bajtami treści, w tej samej kolejności.
func (c *checker) finish() bool {
	c.last = true
	for c.err == nil && !c.end {
		c.step()
	}
	return c.err == nil && c.inf.Consumed() == c.fed && c.rp == len(c.ref)
}
