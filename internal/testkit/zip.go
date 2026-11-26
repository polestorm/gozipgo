// Package testkit to narzędzia testów: budowniczy archiwów ZIP (ten plik) i wzorzec publicznego API (api.go).
// Pakiet służy tylko testom; biblioteka go nie importuje.
//
// Budowniczy składa archiwum rekord po rekordzie. Build układa poprawne archiwum z opisu plików i zwraca je jako
// strukturę rekordów z jawną wartością każdego pola (APPNOTE 4.3.7–4.3.16); test zmienia potem dowolne pole, także
// na wartość sprzeczną z resztą, i dopiero Bytes zapisuje bajty. Nic nie jest poprawiane po cichu: po zmianie
// długości nazwy albo liczby nagłówków pola zależne poprawia test albo jawne wywołanie FixDirectory.
//
// Budowniczy jest pisany z APPNOTE 6.3.10 i RFC 1951 (blok niezakodowany), bez archive/zip i compress/flate.
package testkit

import (
	"encoding/binary"
	"hash/crc32"
	"unicode/utf8"

	"github.com/polestorm/gozipgo/internal/format"
)

// File opisuje jeden wpis poprawnego archiwum dla Build.
type File struct {
	Name    string
	Data    []byte // treść po rozpakowaniu
	Method  uint16 // 0: Stored; 8: Deflate (bloki niezakodowane); inna wartość: Data zapisane bez zmian
	Flags   uint16 // bity dodawane do flag; bit 3 dopisuje deskryptor danych, bit 11 jest ustawiany dla nazw spoza ASCII
	Comment string
	Extra   []byte // pola dodatkowe, takie same w nagłówku lokalnym i w katalogu
	Zip64   bool   // rozmiary i przesunięcie jako wartości graniczne z polem dodatkowym ZIP64
	ModTime uint16
	ModDate uint16 // 0 oznacza 1980-01-01
}

// Zip64Mode mówi, czy archiwum ma rekordy ZIP64 końca katalogu.
type Zip64Mode int

const (
	Zip64None      Zip64Mode = iota // bez rekordów ZIP64
	Zip64Records                    // rekord i lokalizator ZIP64; zwykły koniec katalogu ma wartości rzeczywiste
	Zip64Sentinels                  // rekord i lokalizator ZIP64; zwykły koniec katalogu ma same wartości graniczne
)

// LocalHeader to nagłówek lokalny (4.3.7).
type LocalHeader struct {
	Signature        uint32
	VersionNeeded    uint16
	Flags            uint16
	Method           uint16
	ModTime          uint16
	ModDate          uint16
	CRC32            uint32
	CompressedSize   uint32
	UncompressedSize uint32
	NameLength       uint16
	ExtraLength      uint16
	Name             []byte
	Extra            []byte
}

// Descriptor to deskryptor danych (4.3.9) w jednej z czterech postaci.
type Descriptor struct {
	HasSignature     bool
	Signature        uint32
	CRC32            uint32
	CompressedSize   uint64
	UncompressedSize uint64
	Zip64            bool // rozmiary 8-bajtowe
}

// Member to obszar wpisu: nagłówek lokalny, dane i deskryptor.
type Member struct {
	Header     LocalHeader
	Data       []byte
	Descriptor *Descriptor
}

// CentralHeader to nagłówek katalogu centralnego (4.3.12).
type CentralHeader struct {
	Signature        uint32
	VersionMadeBy    uint16
	VersionNeeded    uint16
	Flags            uint16
	Method           uint16
	ModTime          uint16
	ModDate          uint16
	CRC32            uint32
	CompressedSize   uint32
	UncompressedSize uint32
	NameLength       uint16
	ExtraLength      uint16
	CommentLength    uint16
	DiskStart        uint16
	InternalAttrs    uint16
	ExternalAttrs    uint32
	LocalOffset      uint32
	Name             []byte
	Extra            []byte
	Comment          []byte
}

// SetName ustawia nazwę i jej długość.
func (h *CentralHeader) SetName(b []byte) { h.Name, h.NameLength = b, uint16(len(b)) }

// SetExtra ustawia pola dodatkowe i ich długość.
func (h *CentralHeader) SetExtra(b []byte) { h.Extra, h.ExtraLength = b, uint16(len(b)) }

// SetComment ustawia komentarz i jego długość.
func (h *CentralHeader) SetComment(b []byte) { h.Comment, h.CommentLength = b, uint16(len(b)) }

// Zip64End to rekord końca katalogu ZIP64 (4.3.14).
type Zip64End struct {
	Signature     uint32
	RecordSize    uint64
	VersionMadeBy uint16
	VersionNeeded uint16
	Disk          uint32
	DirDisk       uint32
	EntriesOnDisk uint64
	Entries       uint64
	DirSize       uint64
	DirOffset     uint64
	Data          []byte // sektor danych rozszerzalnych
}

// Zip64Locator to lokalizator końca katalogu ZIP64 (4.3.15).
type Zip64Locator struct {
	Signature uint32
	Disk      uint32
	Offset    uint64
	Disks     uint32
}

// End to rekord końca katalogu centralnego (4.3.16).
type End struct {
	Signature     uint32
	Disk          uint16
	DirDisk       uint16
	EntriesOnDisk uint16
	Entries       uint16
	DirSize       uint32
	DirOffset     uint32
	CommentLength uint16
	Comment       []byte
}

// SetComment ustawia komentarz archiwum i jego długość.
func (e *End) SetComment(b []byte) { e.Comment, e.CommentLength = b, uint16(len(b)) }

// Archive to archiwum jako ciąg rekordów. Bytes zapisuje je w kolejności pól struktury.
type Archive struct {
	Prefix       []byte // bajty przed pierwszym wpisem
	Members      []Member
	BeforeDir    []byte // bajty między ostatnim wpisem a katalogiem
	Central      []CentralHeader
	AfterCentral []byte // bajty po ostatnim nagłówku katalogu (np. rekord podpisu cyfrowego)
	Zip64End     *Zip64End
	Zip64Locator *Zip64Locator
	End          End
	Trailer      []byte // bajty po komentarzu archiwum
}

// Zip buduje poprawne archiwum bez rekordów ZIP64.
func Zip(files ...File) *Archive { return Build(Zip64None, files...) }

// Build buduje poprawne archiwum: wpisy w podanej kolejności, katalog w tej samej kolejności, wszystkie pola spójne.
func Build(mode Zip64Mode, files ...File) *Archive {
	a := &Archive{}
	offset := uint64(0)
	for _, f := range files {
		name := []byte(f.Name)
		flags := f.Flags
		for _, c := range name {
			if c >= 0x80 && utf8.Valid(name) {
				flags |= format.FlagUTF8
				break
			}
		}
		data := f.Data
		if f.Method == format.MethodDeflate {
			data = StoredDeflate(f.Data)
		}
		crc := crc32.ChecksumIEEE(f.Data)
		csize, usize := uint64(len(data)), uint64(len(f.Data))
		version := uint16(10)
		if f.Method == format.MethodDeflate || (len(name) > 0 && name[len(name)-1] == '/') {
			version = 20
		}
		date := f.ModDate
		if date == 0 {
			date = DOSDate(1980, 1, 1)
		}
		lh := LocalHeader{
			Signature: format.SigLocalHeader, VersionNeeded: version, Flags: flags, Method: f.Method,
			ModTime: f.ModTime, ModDate: date, CRC32: crc, CompressedSize: uint32(csize), UncompressedSize: uint32(usize),
			Name: name, NameLength: uint16(len(name)),
		}
		ch := CentralHeader{
			Signature: format.SigCentralHeader, VersionMadeBy: 3<<8 | 20, VersionNeeded: version, Flags: flags,
			Method: f.Method, ModTime: f.ModTime, ModDate: date, CRC32: crc, CompressedSize: uint32(csize),
			UncompressedSize: uint32(usize), LocalOffset: uint32(offset),
		}
		ch.SetName(name)
		ch.SetComment([]byte(f.Comment))
		localExtra, centralExtra := f.Extra, f.Extra
		if f.Zip64 {
			lh.VersionNeeded, ch.VersionNeeded = format.VersionZip64, format.VersionZip64
			lh.CompressedSize, lh.UncompressedSize = format.Sentinel32, format.Sentinel32
			ch.CompressedSize, ch.UncompressedSize, ch.LocalOffset = format.Sentinel32, format.Sentinel32, format.Sentinel32
			localExtra = append(Extra(format.ExtraZip64, U64(usize), U64(csize)), f.Extra...)
			centralExtra = append(Extra(format.ExtraZip64, U64(usize), U64(csize), U64(offset)), f.Extra...)
		}
		lh.Extra, lh.ExtraLength = localExtra, uint16(len(localExtra))
		ch.SetExtra(centralExtra)
		m := Member{Header: lh, Data: data}
		if flags&format.FlagDataDescriptor != 0 {
			m.Header.CRC32, m.Header.CompressedSize, m.Header.UncompressedSize = 0, 0, 0
			if f.Zip64 {
				m.Header.CompressedSize, m.Header.UncompressedSize = format.Sentinel32, format.Sentinel32
			}
			m.Descriptor = &Descriptor{HasSignature: true, Signature: format.SigDataDescriptor, CRC32: crc,
				CompressedSize: csize, UncompressedSize: usize, Zip64: f.Zip64}
		}
		a.Members = append(a.Members, m)
		a.Central = append(a.Central, ch)
		offset += uint64(len(m.Bytes()))
	}
	a.End.Signature = format.SigEnd
	if mode != Zip64None {
		a.Zip64End = &Zip64End{Signature: format.SigZip64End, RecordSize: format.Zip64EndSizeField,
			VersionMadeBy: 3<<8 | format.VersionZip64, VersionNeeded: format.VersionZip64}
		a.Zip64Locator = &Zip64Locator{Signature: format.SigZip64Locator, Disks: 1}
	}
	if mode == Zip64Sentinels {
		a.End.Disk, a.End.DirDisk, a.End.EntriesOnDisk, a.End.Entries = 0, 0, format.Sentinel16, format.Sentinel16
		a.End.DirSize, a.End.DirOffset = format.Sentinel32, format.Sentinel32
	}
	a.FixDirectory()
	return a
}

// DirOffset zwraca rzeczywiste przesunięcie pierwszego nagłówka katalogu w bajtach archiwum.
func (a *Archive) DirOffset() uint64 {
	n := uint64(len(a.Prefix))
	for i := range a.Members {
		n += uint64(len(a.Members[i].Bytes()))
	}
	return n + uint64(len(a.BeforeDir))
}

// DirSize zwraca rzeczywisty łączny rozmiar nagłówków katalogu (bez AfterCentral).
func (a *Archive) DirSize() uint64 {
	n := uint64(0)
	for i := range a.Central {
		n += uint64(len(a.Central[i].Bytes()))
	}
	return n
}

// EndOffset zwraca rzeczywiste przesunięcie zwykłego rekordu końca katalogu.
func (a *Archive) EndOffset() uint64 {
	n := a.DirOffset() + a.DirSize() + uint64(len(a.AfterCentral))
	if a.Zip64End != nil {
		n += uint64(len(a.Zip64End.Bytes()))
	}
	if a.Zip64Locator != nil {
		n += format.Zip64LocatorSize
	}
	return n
}

// FixDirectory wpisuje do rekordów końca katalogu rzeczywistą liczbę nagłówków, rozmiar i przesunięcie katalogu
// oraz położenie rekordu ZIP64 w lokalizatorze. Pole zwykłego rekordu, które ma wartość graniczną, zostaje
// graniczne, o ile archiwum ma rekord ZIP64.
func (a *Archive) FixDirectory() {
	n, size, offset := uint64(len(a.Central)), a.DirSize(), a.DirOffset()
	z := a.Zip64End != nil
	if !(z && a.End.EntriesOnDisk == format.Sentinel16) {
		a.End.EntriesOnDisk = uint16(n)
	}
	if !(z && a.End.Entries == format.Sentinel16) {
		a.End.Entries = uint16(n)
	}
	if !(z && a.End.DirSize == format.Sentinel32) {
		a.End.DirSize = uint32(size)
	}
	if !(z && a.End.DirOffset == format.Sentinel32) {
		a.End.DirOffset = uint32(offset)
	}
	if z {
		a.Zip64End.EntriesOnDisk, a.Zip64End.Entries, a.Zip64End.DirSize, a.Zip64End.DirOffset = n, n, size, offset
	}
	if a.Zip64Locator != nil {
		a.Zip64Locator.Offset = offset + size + uint64(len(a.AfterCentral))
	}
}

// Bytes zapisuje archiwum.
func (a *Archive) Bytes() []byte {
	b := append([]byte(nil), a.Prefix...)
	for i := range a.Members {
		b = append(b, a.Members[i].Bytes()...)
	}
	b = append(b, a.BeforeDir...)
	for i := range a.Central {
		b = append(b, a.Central[i].Bytes()...)
	}
	b = append(b, a.AfterCentral...)
	if a.Zip64End != nil {
		b = append(b, a.Zip64End.Bytes()...)
	}
	if a.Zip64Locator != nil {
		b = append(b, a.Zip64Locator.Bytes()...)
	}
	b = append(b, a.End.Bytes()...)
	return append(b, a.Trailer...)
}

// Bytes zapisuje obszar wpisu.
func (m *Member) Bytes() []byte {
	h := &m.Header
	b := U32(h.Signature)
	b = append(b, U16(h.VersionNeeded)...)
	b = append(b, U16(h.Flags)...)
	b = append(b, U16(h.Method)...)
	b = append(b, U16(h.ModTime)...)
	b = append(b, U16(h.ModDate)...)
	b = append(b, U32(h.CRC32)...)
	b = append(b, U32(h.CompressedSize)...)
	b = append(b, U32(h.UncompressedSize)...)
	b = append(b, U16(h.NameLength)...)
	b = append(b, U16(h.ExtraLength)...)
	b = append(b, h.Name...)
	b = append(b, h.Extra...)
	b = append(b, m.Data...)
	if d := m.Descriptor; d != nil {
		if d.HasSignature {
			b = append(b, U32(d.Signature)...)
		}
		b = append(b, U32(d.CRC32)...)
		if d.Zip64 {
			b = append(b, U64(d.CompressedSize)...)
			b = append(b, U64(d.UncompressedSize)...)
		} else {
			b = append(b, U32(uint32(d.CompressedSize))...)
			b = append(b, U32(uint32(d.UncompressedSize))...)
		}
	}
	return b
}

// Bytes zapisuje nagłówek katalogu.
func (h *CentralHeader) Bytes() []byte {
	b := U32(h.Signature)
	b = append(b, U16(h.VersionMadeBy)...)
	b = append(b, U16(h.VersionNeeded)...)
	b = append(b, U16(h.Flags)...)
	b = append(b, U16(h.Method)...)
	b = append(b, U16(h.ModTime)...)
	b = append(b, U16(h.ModDate)...)
	b = append(b, U32(h.CRC32)...)
	b = append(b, U32(h.CompressedSize)...)
	b = append(b, U32(h.UncompressedSize)...)
	b = append(b, U16(h.NameLength)...)
	b = append(b, U16(h.ExtraLength)...)
	b = append(b, U16(h.CommentLength)...)
	b = append(b, U16(h.DiskStart)...)
	b = append(b, U16(h.InternalAttrs)...)
	b = append(b, U32(h.ExternalAttrs)...)
	b = append(b, U32(h.LocalOffset)...)
	b = append(b, h.Name...)
	b = append(b, h.Extra...)
	return append(b, h.Comment...)
}

// Bytes zapisuje rekord końca katalogu ZIP64.
func (z *Zip64End) Bytes() []byte {
	b := U32(z.Signature)
	b = append(b, U64(z.RecordSize)...)
	b = append(b, U16(z.VersionMadeBy)...)
	b = append(b, U16(z.VersionNeeded)...)
	b = append(b, U32(z.Disk)...)
	b = append(b, U32(z.DirDisk)...)
	b = append(b, U64(z.EntriesOnDisk)...)
	b = append(b, U64(z.Entries)...)
	b = append(b, U64(z.DirSize)...)
	b = append(b, U64(z.DirOffset)...)
	return append(b, z.Data...)
}

// Bytes zapisuje lokalizator ZIP64.
func (l *Zip64Locator) Bytes() []byte {
	b := U32(l.Signature)
	b = append(b, U32(l.Disk)...)
	b = append(b, U64(l.Offset)...)
	return append(b, U32(l.Disks)...)
}

// Bytes zapisuje rekord końca katalogu.
func (e *End) Bytes() []byte {
	b := U32(e.Signature)
	b = append(b, U16(e.Disk)...)
	b = append(b, U16(e.DirDisk)...)
	b = append(b, U16(e.EntriesOnDisk)...)
	b = append(b, U16(e.Entries)...)
	b = append(b, U32(e.DirSize)...)
	b = append(b, U32(e.DirOffset)...)
	b = append(b, U16(e.CommentLength)...)
	return append(b, e.Comment...)
}

// U16, U32 i U64 zapisują liczbę w kolejności little-endian (4.4.1.1).
func U16(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }

// U32 zapisuje liczbę 32-bitową.
func U32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// U64 zapisuje liczbę 64-bitową.
func U64(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

// Extra zapisuje jedno pole dodatkowe: identyfikator, długość danych i dane sklejone z części (4.5.1).
func Extra(id uint16, parts ...[]byte) []byte {
	var data []byte
	for _, p := range parts {
		data = append(data, p...)
	}
	b := append(U16(id), U16(uint16(len(data)))...)
	return append(b, data...)
}

// UnicodePath zapisuje pole dodatkowe Info-ZIP Unicode Path (4.6.9) dla nazwy unicodeName i pola nazwy headerName.
func UnicodePath(unicodeName string, headerName []byte) []byte {
	return Extra(format.ExtraUnicodePath, []byte{1}, U32(crc32.ChecksumIEEE(headerName)), []byte(unicodeName))
}

// StoredDeflate zapisuje dane jako strumień DEFLATE z samych bloków niezakodowanych (RFC 1951, 3.2.4): nagłówek
// bloku w jednym bajcie (BFINAL w bicie 0, BTYPE 00), potem LEN i NLEN. Puste dane dają jeden pusty blok końcowy.
func StoredDeflate(data []byte) []byte {
	var b []byte
	for {
		n := min(len(data), 0xFFFF)
		final := byte(0)
		if n == len(data) {
			final = 1
		}
		b = append(b, final)
		b = append(b, U16(uint16(n))...)
		b = append(b, U16(^uint16(n))...)
		b = append(b, data[:n]...)
		data = data[n:]
		if final == 1 {
			return b
		}
	}
}

// DOSDate zapisuje datę w formacie MS-DOS: rok od 1980 w bitach 9–15, miesiąc w 5–8, dzień w 0–4.
func DOSDate(year, month, day int) uint16 { return uint16((year-1980)<<9 | month<<5 | day) }

// DOSTime zapisuje czas w formacie MS-DOS: godzina w bitach 11–15, minuta w 5–10, sekundy dzielone przez 2 w 0–4.
func DOSTime(hour, minute, second int) uint16 { return uint16(hour<<11 | minute<<5 | second/2) }
