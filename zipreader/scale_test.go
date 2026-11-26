package zipreader

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// bestOf zwraca najkrótszy z kilku pomiarów f.
func bestOf(n int, f func()) time.Duration {
	best := time.Duration(1 << 62)
	for i := 0; i < n; i++ {
		start := time.Now()
		f()
		best = min(best, time.Since(start))
	}
	return best
}

// allocated zwraca liczbę bajtów zaalokowanych w czasie f.
func allocated(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func drain(t testing.TB, r *Reader, i int) int64 {
	rc, err := r.Open(r.Entries()[i])
	if err != nil {
		t.Fatal(err)
	}
	// Własny bufor: io.Copy bierze bufor z puli, więc jego alokacja zależałaby od historii przebiegu.
	var n int64
	for {
		k, err := rc.Read(drainBuffer)
		n += int64(k)
		if err == io.EOF {
			return n
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

var drainBuffer = make([]byte, 32<<10)

// TestLinearTime: podwojenie rozmiaru danych albo liczby wpisów zwiększa czas odczytu treści i pełnego New
// (z nagłówkami lokalnymi) najwyżej 2,5 raza; próg testu to 3,5, z zapasem na szum pomiaru.
// Wpis Stored nie jest tu mierzony: jego odczyt to kopiowanie i CRC-32, trwa około milisekundy na 16 MiB, czyli
// mniej niż rozdzielczość zegara na Windows, i zależy od pamięci podręcznej procesora bardziej niż od kodu.
// REQ: SAFE-005
func TestLinearTime(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	read := func(file testkit.File) func() {
		data := testkit.Zip(file).Bytes()
		r, err := open(data, Options{MaxRatio: -1})
		if err != nil {
			t.Fatal(err)
		}
		return func() { drain(t, r, 0) }
	}
	zeros := func(n int) testkit.File {
		stream, size := zeroStream(n)
		return testkit.File{Name: "z", Data: make([]byte, size), Method: format.MethodDeflate, Raw: stream}
	}
	text := func(n int) testkit.File { return deflateFile("t", pattern(n), 6) }
	layout := func(n int) func() {
		fs := make([]testkit.File, n)
		for i := range fs {
			fs[i] = testkit.File{Name: fmt.Sprintf("dir/%07d", (i*7919)%n), Data: []byte("x"), Flags: format.FlagDataDescriptor}
		}
		data := testkit.Build(testkit.Zip64Sentinels, fs...).Bytes()
		return func() {
			if _, err := open(data, Options{MaxEntries: -1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []struct {
		label        string
		small, large func()
	}{
		{"content, highest ratio", read(zeros(32 << 20)), read(zeros(64 << 20))},
		{"content, text", read(text(4 << 20)), read(text(8 << 20))},
		{"New with local headers", layout(20000), layout(40000)},
	} {
		small, large := bestOf(5, c.small), bestOf(5, c.large)
		ratio := float64(large) / float64(small)
		t.Logf("%s: %v and %v, ratio %.2f", c.label, small, large, ratio)
		if ratio > 3.5 {
			t.Errorf("%s: doubling the input multiplied the time by %.2f", c.label, ratio)
		}
	}
}

// TestMemory: pamięć New zależy od liczby wpisów, nie od rozmiaru danych; pamięć odczytu wpisu jest stała.
// REQ: SAFE-006
func TestMemory(t *testing.T) {
	archive := func(entries, size int) []byte {
		fs := make([]testkit.File, entries)
		body := pattern(size)
		for i := range fs {
			fs[i] = testkit.File{Name: fmt.Sprintf("f%04d", i), Data: body}
		}
		return testkit.Zip(fs...).Bytes()
	}
	newAlloc := func(data []byte) uint64 {
		return allocated(func() {
			if _, err := open(data, Options{}); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Oba archiwa są większe niż stałe bufory New (koniec pliku 64 KiB, nagłówki lokalne 8 KiB).
	small, large := newAlloc(archive(100, 10000)), newAlloc(archive(100, 1000000))
	t.Logf("New, 100 entries: %d bytes with 10 kB entries, %d bytes with 1 MB entries", small, large)
	if large > small+small/4+4096 {
		t.Errorf("New allocates %d bytes for small data and %d for large data", small, large)
	}
	few, many := newAlloc(archive(100, 10)), newAlloc(archive(1000, 10))
	t.Logf("New: %d bytes for 100 entries, %d for 1000: %d bytes per entry", few, many, (many-few)/900)
	if (many-few)/900 > 1024 {
		t.Errorf("New allocates %d bytes per entry", (many-few)/900)
	}
	// Odczyt: 1 MiB i 32 MiB treści, Deflate i Stored.
	for _, method := range []uint16{format.MethodDeflate, format.MethodStored} {
		var got [2]uint64
		for k, size := range []int{1 << 20, 32 << 20} {
			f := testkit.File{Name: "a", Data: pattern(size), Method: method}
			if method == format.MethodDeflate {
				f = deflateFile("a", f.Data, 6)
			}
			r, err := open(testkit.Zip(f).Bytes(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			got[k] = allocated(func() { drain(t, r, 0) })
		}
		t.Logf("reading, method %d: %d bytes allocated for 1 MiB, %d for 32 MiB", method, got[0], got[1])
		if got[1] > got[0]+4096 || got[1] > 256<<10 {
			t.Errorf("method %d: reading allocates %d bytes for 1 MiB and %d for 32 MiB", method, got[0], got[1])
		}
	}
}

// segment to kawałek syntetycznego pliku: bajty albo ciąg zer o podanej długości.
type segment struct {
	off   int64
	data  []byte
	zeros int64
}

func (s segment) size() int64 {
	if s.data != nil {
		return int64(len(s.data))
	}
	return s.zeros
}

// synthetic to io.ReaderAt złożony z segmentów; nie trzyma w pamięci ciągów zer.
type synthetic struct {
	segs []segment
	size int64
}

func (s *synthetic) add(data []byte) {
	s.segs = append(s.segs, segment{off: s.size, data: data})
	s.size += int64(len(data))
}
func (s *synthetic) addZeros(n int64) {
	s.segs = append(s.segs, segment{off: s.size, zeros: n})
	s.size += n
}

func (s *synthetic) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	i := sort.Search(len(s.segs), func(i int) bool { return s.segs[i].off+s.segs[i].size() > off })
	n := 0
	for ; i < len(s.segs) && n < len(p); i++ {
		g := s.segs[i]
		at := off + int64(n) - g.off
		k := int(min(int64(len(p)-n), g.size()-at))
		if g.data != nil {
			copy(p[n:n+k], g.data[at:])
		} else {
			clear(p[n : n+k])
		}
		n += k
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// zeroCRC liczy CRC-32 n bajtów zerowych.
func zeroCRC(n int64) uint32 {
	chunk := make([]byte, 1<<20)
	crc := uint32(0)
	for ; n > 0; n -= int64(len(chunk)) {
		if n < int64(len(chunk)) {
			chunk = chunk[:n]
		}
		crc = crc32.Update(crc, crc32.IEEETable, chunk)
	}
	return crc
}

// TestLargeArchive mierzy część kryterium K7: archiwum ZIP64 większe niż 4 GiB, z wpisem Stored ponad 4 GiB
// i wpisem Deflate ponad 4 GiB treści położonym za czwartym gigabajtem pliku, czytane ze źródła syntetycznego
// (bez pliku na dysku) w całości, z kontrolą rozmiaru i CRC-32, przy alokacjach poniżej 64 MiB. Do tego archiwum
// o 70 000 wpisów, czyli ponad to, co mieści zwykły rekord końca katalogu.
// REQ: SAFE-006 DIR-005
func TestLargeArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("reads more than 8 GiB of synthetic content")
	}
	const storedSize = 4<<30 + 12345
	stream, deflatedSize := zeroStream(4<<30 + 1<<20)
	src := &synthetic{}
	var central []byte
	entry := func(name string, method uint16, csize, usize int64, crc uint32, body func()) {
		offset := src.size
		local := testkit.Member{Header: testkit.LocalHeader{Signature: format.SigLocalHeader, VersionNeeded: format.VersionZip64,
			Method: method, ModDate: testkit.DOSDate(1980, 1, 1), CRC32: crc, CompressedSize: format.Sentinel32,
			UncompressedSize: format.Sentinel32, Name: []byte(name), NameLength: uint16(len(name))}}
		x := testkit.Extra(format.ExtraZip64, testkit.U64(uint64(usize)), testkit.U64(uint64(csize)))
		local.Header.Extra, local.Header.ExtraLength = x, uint16(len(x))
		src.add(local.Bytes())
		body()
		ch := testkit.CentralHeader{Signature: format.SigCentralHeader, VersionMadeBy: 3<<8 | format.VersionZip64,
			VersionNeeded: format.VersionZip64, Method: method, ModDate: testkit.DOSDate(1980, 1, 1), CRC32: crc,
			CompressedSize: format.Sentinel32, UncompressedSize: format.Sentinel32, LocalOffset: format.Sentinel32}
		ch.SetName([]byte(name))
		ch.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(uint64(usize)), testkit.U64(uint64(csize)), testkit.U64(uint64(offset))))
		central = append(central, ch.Bytes()...)
	}
	entry("stored.bin", format.MethodStored, storedSize, storedSize, zeroCRC(storedSize), func() { src.addZeros(storedSize) })
	entry("deflated.bin", format.MethodDeflate, int64(len(stream)), int64(deflatedSize), zeroCRC(int64(deflatedSize)), func() { src.add(stream) })
	dirOffset := src.size
	src.add(central)
	z := testkit.Zip64End{Signature: format.SigZip64End, RecordSize: format.Zip64EndSizeField, VersionMadeBy: 3<<8 | format.VersionZip64,
		VersionNeeded: format.VersionZip64, EntriesOnDisk: 2, Entries: 2, DirSize: uint64(len(central)), DirOffset: uint64(dirOffset)}
	loc := testkit.Zip64Locator{Signature: format.SigZip64Locator, Offset: uint64(src.size), Disks: 1}
	src.add(z.Bytes())
	src.add(loc.Bytes())
	end := testkit.End{Signature: format.SigEnd, EntriesOnDisk: 2, Entries: 2, DirSize: uint32(len(central)), DirOffset: format.Sentinel32}
	src.add(end.Bytes())

	start := time.Now()
	var r *Reader
	var read [2]int64
	bytesAllocated := allocated(func() {
		var err error
		if r, err = New(src, src.size, Options{MaxEntrySize: -1, MaxTotalSize: -1, MaxRatio: -1}); err != nil {
			t.Fatal(err)
		}
		for i := range read {
			read[i] = drain(t, r, i)
		}
	})
	if read[0] != storedSize || read[1] != int64(deflatedSize) || r.Entries()[1].LocalHeaderOffset < 4<<30 {
		t.Errorf("read %d and %d bytes; the second entry is at %d", read[0], read[1], r.Entries()[1].LocalHeaderOffset)
	}
	t.Logf("archive of %d bytes, %d bytes of content read in %v, %d KiB allocated", src.size, read[0]+read[1], time.Since(start), bytesAllocated>>10)
	if bytesAllocated > 64<<20 {
		t.Errorf("%d bytes allocated; the limit is 64 MiB", bytesAllocated)
	}
	// Domyślne limity odmawiają obu wpisów, zanim cokolwiek zostanie przeczytane.
	strict, err := New(src, src.size, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range strict.Entries() {
		if _, err := strict.Open(e); codeOf(err) != CodeLimitEntrySize {
			t.Errorf("entry %d with default limits: %v", i, err)
		}
	}
	// Ponad 65 535 wpisów.
	many := make([]testkit.File, 70000)
	for i := range many {
		many[i] = testkit.File{Name: fmt.Sprintf("%05d", i), Data: []byte{byte(i)}}
	}
	data := testkit.Build(testkit.Zip64Sentinels, many...).Bytes()
	wantFullB(t, "70,000 entries, default limit", data, Options{}, CodeLimitEntries)
	big, err := New(bytes.NewReader(data), int64(len(data)), Options{MaxEntries: -1})
	if err != nil || len(big.Entries()) != len(many) {
		t.Fatalf("70,000 entries: %v", err)
	}
	if b, err := readEntry(big, big.Entries()[69999]); err != nil || len(b) != 1 || b[0] != byte(69999&0xFF) {
		t.Errorf("last of 70,000 entries: %v, %v", b, err)
	}
}

// wantFullB to wantFull dla testing.TB.
func wantFullB(t testing.TB, label string, data []byte, opts Options, code string) {
	t.Helper()
	if _, err := open(data, opts); codeOf(err) != code {
		t.Errorf("%s: got %v, want %q", label, err, code)
	}
}
