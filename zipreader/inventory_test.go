package zipreader

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// inventoryStage to ostatni wykonany etap planu: wiersze inwentarza z wyższym etapem są liczone jako oczekujące.
const inventoryStage = 2

// inventoryCase to archiwa pokazujące jedną cechę z inwentarza i sprawdzenie tego, czego sam werdykt nie mówi.
type inventoryCase struct {
	archives [][]byte
	check    func(r *Reader) string // dla werdyktów accept i expose: pusty napis, gdy cecha jest obsłużona
}

// base to najmniejsze poprawne archiwum z jednym wpisem; mut zmienia je przed zapisem.
func base(mut func(a *testkit.Archive)) []byte {
	a := testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("hello")})
	if mut != nil {
		mut(a)
	}
	return a.Bytes()
}

// zip64Base to to samo archiwum z rekordami ZIP64 i wartościami granicznymi w zwykłym końcu katalogu.
func zip64Base(mode testkit.Zip64Mode, mut func(a *testkit.Archive)) []byte {
	a := testkit.Build(mode, testkit.File{Name: "a.txt", Data: []byte("hello")})
	if mut != nil {
		mut(a)
	}
	return a.Bytes()
}

// header zmienia nagłówek katalogu jedynego wpisu; pola końca katalogu zostają, jak były.
func header(mut func(h *testkit.CentralHeader)) []byte {
	return base(func(a *testkit.Archive) { mut(&a.Central[0]) })
}

// resized zmienia nagłówek katalogu i poprawia rozmiar katalogu w rekordzie końca.
func resized(mut func(h *testkit.CentralHeader)) []byte {
	return base(func(a *testkit.Archive) { mut(&a.Central[0]); a.FixDirectory() })
}

func one(b []byte) inventoryCase { return inventoryCase{archives: [][]byte{b}} }

func entryCheck(f func(e Entry) bool) func(r *Reader) string {
	return func(r *Reader) string {
		if len(r.Entries()) != 1 || !f(r.Entries()[0]) {
			return fmt.Sprintf("the value is not exposed as stored: %+v", r.Entries())
		}
		return ""
	}
}

func inventoryCases() map[string]inventoryCase {
	c := map[string]inventoryCase{}

	// Rekordy.
	c["record.encryption-header"] = one(testkit.Zip(testkit.File{Name: "a.txt", Flags: format.FlagEncrypted,
		Data: append(make([]byte, 12), "hello"...)}).Bytes())
	v2 := func(a *testkit.Archive) {
		// Pola wersji 2 (7.3.4): metoda, rozmiar skompresowany, rozmiar pierwotny, AlgId, BitLen, Flags.
		a.Zip64End.Data = make([]byte, 24)
		a.Zip64End.RecordSize += 24
		a.Zip64End.VersionNeeded = 62
		a.FixDirectory()
	}
	c["record.archive-decryption-header"] = one(zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) {
		a.BeforeDir = make([]byte, 20) // nagłówek odszyfrowania nie ma sygnatury (7.2.4)
		v2(a)
	}))
	c["record.archive-extra-data"] = one(base(func(a *testkit.Archive) {
		rec := append(testkit.U32(format.SigArchiveExtraData), testkit.U32(4)...)
		a.BeforeDir = append(rec, 1, 2, 3, 4)
		a.FixDirectory()
		a.End.DirOffset -= uint32(len(a.BeforeDir)) // rekord należy do katalogu (4.3.11.2)
		a.End.DirSize += uint32(len(a.BeforeDir))
	}))
	c["record.central-header"] = one(base(nil))
	c["record.digital-signature"] = one(base(func(a *testkit.Archive) {
		a.AfterCentral = append(append(testkit.U32(format.SigDigitalSignature), testkit.U16(4)...), 1, 2, 3, 4)
		a.End.DirSize += uint32(len(a.AfterCentral))
	}))
	c["record.zip64-end"] = one(zip64Base(testkit.Zip64Sentinels, nil))
	c["record.zip64-end-v2"] = one(zip64Base(testkit.Zip64Sentinels, v2))
	c["record.zip64-extensible-data"] = one(zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) {
		a.Zip64End.Data = append(append(testkit.U16(0x0065), testkit.U32(4)...), 1, 2, 3, 4)
		a.Zip64End.RecordSize += uint64(len(a.Zip64End.Data))
	}))
	c["record.zip64-locator"] = one(zip64Base(testkit.Zip64Sentinels, nil))
	c["record.end"] = one(base(nil))
	c["record.end.absent"] = one(base(func(a *testkit.Archive) { a.End.Signature = 0 }))
	c["record.end.second"] = one(base(func(a *testkit.Archive) {
		inner := a.End // drugi rekord w komentarzu pierwszego; oba kończą się równo z plikiem
		a.End.SetComment(inner.Bytes())
	}))
	c["record.end.only"] = inventoryCase{archives: [][]byte{testkit.Zip().Bytes()}, check: func(r *Reader) string {
		if len(r.Entries()) != 0 {
			return "an archive with only the end record must have no entries"
		}
		return ""
	}}

	// Nagłówek katalogu centralnego.
	c["cdh.signature"] = one(header(func(h *testkit.CentralHeader) { h.Signature = 0x12345678 }))
	expose := func(key string, mut func(h *testkit.CentralHeader), ok func(e Entry) bool) {
		c[key] = inventoryCase{archives: [][]byte{header(mut)}, check: entryCheck(ok)}
	}
	expose("cdh.version-made-by", func(h *testkit.CentralHeader) { h.VersionMadeBy = 0x0B3F },
		func(e Entry) bool { return e.VersionMadeBy == 0x0B3F })
	expose("cdh.version-needed", func(h *testkit.CentralHeader) { h.VersionNeeded = 31 },
		func(e Entry) bool { return e.VersionNeeded == 31 })
	expose("cdh.flags", func(h *testkit.CentralHeader) { h.Flags = 6 }, func(e Entry) bool { return e.Flags == 6 })
	expose("cdh.method", func(h *testkit.CentralHeader) { h.Method = 8 }, func(e Entry) bool { return e.Method == 8 })
	expose("cdh.mod-time", func(h *testkit.CentralHeader) { h.ModTime = testkit.DOSTime(13, 37, 58) },
		func(e Entry) bool {
			m := e.Modified
			return m.Hour == 13 && m.Minute == 37 && m.Second == 58 && m.Valid
		})
	expose("cdh.mod-date", func(h *testkit.CentralHeader) { h.ModDate = testkit.DOSDate(2026, 11, 26) },
		func(e Entry) bool { m := e.Modified; return m.Year == 2026 && m.Month == 11 && m.Day == 26 && m.Valid })
	expose("cdh.mod-date.invalid", func(h *testkit.CentralHeader) { h.ModDate = 0 },
		func(e Entry) bool { m := e.Modified; return m.Year == 1980 && m.Month == 0 && m.Day == 0 && !m.Valid })
	expose("cdh.crc32", func(h *testkit.CentralHeader) { h.CRC32 = 0xDEADBEEF },
		func(e Entry) bool { return e.CRC32 == 0xDEADBEEF })
	expose("cdh.compressed-size", func(h *testkit.CentralHeader) { h.CompressedSize = 12345 },
		func(e Entry) bool { return e.CompressedSize == 12345 })
	expose("cdh.uncompressed-size", func(h *testkit.CentralHeader) { h.UncompressedSize = 54321 },
		func(e Entry) bool { return e.UncompressedSize == 54321 })
	c["cdh.name-length"] = one(header(func(h *testkit.CentralHeader) { h.NameLength = 0xFFFF }))
	c["cdh.extra-length"] = one(header(func(h *testkit.CentralHeader) { h.ExtraLength = 1 }))
	c["cdh.comment-length"] = one(header(func(h *testkit.CentralHeader) { h.CommentLength = 0xFFFF }))
	c["cdh.disk-start"] = one(header(func(h *testkit.CentralHeader) { h.DiskStart = 1 }))
	expose("cdh.internal-attrs", func(h *testkit.CentralHeader) { h.InternalAttrs = 0x0003 },
		func(e Entry) bool { return e.InternalAttrs == 0x0003 })
	expose("cdh.external-attrs", func(h *testkit.CentralHeader) { h.ExternalAttrs = 0x81A40020 },
		func(e Entry) bool { return e.ExternalAttrs == 0x81A40020 })
	expose("cdh.local-offset", func(h *testkit.CentralHeader) { h.LocalOffset = 0x1234 },
		func(e Entry) bool { return e.LocalHeaderOffset == 0x1234 })
	name := func(s string) []byte { return resized(func(h *testkit.CentralHeader) { h.SetName([]byte(s)) }) }
	c["cdh.name"] = inventoryCase{archives: [][]byte{name("dir/sub/file name.txt")},
		check: entryCheck(func(e Entry) bool { return e.Name == "dir/sub/file name.txt" && !e.IsDir })}
	c["cdh.name.empty"] = one(name(""))
	c["cdh.name.leading-slash"] = one(name("/etc/passwd"))
	c["cdh.name.drive-letter"] = inventoryCase{archives: [][]byte{name("C:/x"), name("c:x")}}
	c["cdh.name.backslash"] = one(name(`dir\file`))
	c["cdh.name.unflagged-non-ascii"] = inventoryCase{archives: [][]byte{name("za\xbf"), name("zażółć")}}
	c["cdh.extra"] = inventoryCase{
		archives: [][]byte{resized(func(h *testkit.CentralHeader) {
			h.SetExtra(append(testkit.Extra(0x5455, []byte{1, 2, 3, 4, 5}), testkit.Extra(0x7875)...))
		})},
		check: entryCheck(func(e Entry) bool {
			return bytes.Equal(e.Extra, append(testkit.Extra(0x5455, []byte{1, 2, 3, 4, 5}), testkit.Extra(0x7875)...))
		}),
	}
	c["cdh.extra.malformed"] = inventoryCase{archives: [][]byte{
		resized(func(h *testkit.CentralHeader) { h.SetExtra([]byte{0x55, 0x54, 10, 0, 1, 2}) }), // blok dłuższy niż obszar
		resized(func(h *testkit.CentralHeader) { h.SetExtra([]byte{0x55, 0x54, 0}) }),           // urwany nagłówek bloku
	}}
	c["cdh.comment"] = inventoryCase{
		archives: [][]byte{resized(func(h *testkit.CentralHeader) { h.SetComment([]byte("note \xff")) })},
		check:    entryCheck(func(e Entry) bool { return e.Comment == "note \xff" }),
	}
	c["cdh.comment.not-utf8"] = one(resized(func(h *testkit.CentralHeader) {
		h.Flags |= format.FlagUTF8
		h.SetComment([]byte("\xff"))
	}))
	c["cdh.directory-with-data"] = inventoryCase{archives: [][]byte{
		testkit.Zip(testkit.File{Name: "d/", Data: []byte("x")}).Bytes(),
		func() []byte { // rozmiar po rozpakowaniu zero, ale dane skompresowane są
			a := testkit.Zip(testkit.File{Name: "d/"})
			a.Central[0].CompressedSize = 2
			return a.Bytes()
		}(),
	}}
	c["cdh.empty-file-with-data"] = inventoryCase{
		archives: [][]byte{testkit.Zip(testkit.File{Name: "e", Method: format.MethodDeflate}).Bytes()},
		check:    entryCheck(func(e Entry) bool { return e.UncompressedSize == 0 && e.CompressedSize == 5 }),
	}

	// Koniec katalogu ZIP64 i lokalizator.
	z := func(mode testkit.Zip64Mode, mut func(a *testkit.Archive)) inventoryCase {
		return one(zip64Base(mode, mut))
	}
	c["z64.signature"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.Signature = 0 })
	c["z64.record-size"] = inventoryCase{archives: [][]byte{
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.RecordSize = 45 }),
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.RecordSize = 43 }),
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.RecordSize = 1 << 63 }),
	}}
	c["z64.version-made-by"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.VersionMadeBy = 0xFFFF })
	c["z64.version-needed"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.VersionNeeded = 46 })
	c["z64.disk"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.End.Disk, a.Zip64End.Disk = format.Sentinel16, 1 })
	c["z64.dir-disk"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.End.DirDisk, a.Zip64End.DirDisk = format.Sentinel16, 1 })
	c["z64.entries-on-disk"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.EntriesOnDisk = 0 })
	c["z64.entries"] = z(testkit.Zip64Records, func(a *testkit.Archive) { a.Zip64End.Entries, a.Zip64End.EntriesOnDisk = 2, 2 })
	c["z64.dir-size"] = z(testkit.Zip64Records, func(a *testkit.Archive) { a.Zip64End.DirSize++ })
	c["z64.dir-offset"] = z(testkit.Zip64Records, func(a *testkit.Archive) { a.Zip64End.DirOffset-- })
	c["loc.signature"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End, a.Zip64Locator = nil, nil })
	c["loc.disk"] = z(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64Locator.Disk = 1 })
	c["loc.offset"] = inventoryCase{archives: [][]byte{
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64Locator.Offset++ }),
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64Locator.Offset = 1 << 62 }),
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64Locator.Offset = ^uint64(0) }),
	}}
	c["loc.disks"] = inventoryCase{archives: [][]byte{
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64Locator.Disks = 2 }),
		zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64Locator.Disks = 0 }),
	}}
	c["loc.unneeded"] = z(testkit.Zip64Records, nil)

	// Koniec katalogu centralnego.
	c["end.signature"] = one(base(func(a *testkit.Archive) { a.End.Signature = 0x06054b51 }))
	c["end.disk"] = one(base(func(a *testkit.Archive) { a.End.Disk = 1 }))
	c["end.dir-disk"] = one(base(func(a *testkit.Archive) { a.End.DirDisk = 1 }))
	c["end.entries-on-disk"] = one(base(func(a *testkit.Archive) { a.End.EntriesOnDisk = 0 }))
	c["end.entries"] = inventoryCase{archives: [][]byte{
		base(func(a *testkit.Archive) { a.End.Entries, a.End.EntriesOnDisk = 2, 2 }), // więcej, niż mieści katalog
		base(func(a *testkit.Archive) { a.End.Entries, a.End.EntriesOnDisk = 0, 0 }), // mniej niż nagłówków
		func() []byte { // mniej niż nagłówków, a katalog pomieściłby zadeklarowaną liczbę
			a := testkit.Zip(testkit.File{Name: "a"}, testkit.File{Name: "b"}, testkit.File{Name: "c"})
			a.End.Entries, a.End.EntriesOnDisk = 2, 2
			return a.Bytes()
		}(),
	}}
	c["end.dir-size"] = inventoryCase{archives: [][]byte{
		base(func(a *testkit.Archive) { a.End.DirSize++ }),
		base(func(a *testkit.Archive) { a.End.DirSize-- }),
	}}
	c["end.dir-offset"] = inventoryCase{archives: [][]byte{
		base(func(a *testkit.Archive) { a.End.DirOffset++ }),
		base(func(a *testkit.Archive) { a.End.DirOffset = 0xFFFFFFF0 }),
	}}
	c["end.comment-length"] = one(base(func(a *testkit.Archive) { a.Trailer = []byte("x") }))
	c["end.comment"] = inventoryCase{
		archives: [][]byte{base(func(a *testkit.Archive) { a.End.SetComment([]byte("archive \xfe comment")) })},
		check: func(r *Reader) string {
			if r.Comment() != "archive \xfe comment" {
				return "the archive comment is not exposed as stored"
			}
			return ""
		},
	}

	// Flaga ogólna: jeden bit w nagłówku katalogu.
	for bit := 0; bit < 16; bit++ {
		c[fmt.Sprintf("flag.%d", bit)] = one(header(func(h *testkit.CentralHeader) { h.Flags |= 1 << bit }))
	}

	// Metody kompresji.
	method := func(m uint16) []byte { return header(func(h *testkit.CentralHeader) { h.Method = m }) }
	for _, m := range []uint16{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 93, 94, 95, 96, 97, 98} {
		c[fmt.Sprintf("method.%d", m)] = one(method(m))
	}
	c["method.99"] = one(testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("hello"), Method: 99,
		Flags: format.FlagEncrypted, Extra: testkit.Extra(format.ExtraAEx, []byte{2, 0, 'A', 'E', 3, 8, 0})}).Bytes())
	c["method.other"] = inventoryCase{archives: [][]byte{method(21), method(92), method(100), method(65535)}}

	// Wersja wymagana.
	version := func(v uint16) []byte { return header(func(h *testkit.CentralHeader) { h.VersionNeeded = v }) }
	for _, v := range []uint16{10, 11, 20, 21, 25, 27, 45, 46, 50, 51, 52, 61, 62, 63} {
		c[fmt.Sprintf("version.%d", v)] = one(version(v))
	}
	c["version.other"] = inventoryCase{archives: [][]byte{version(47), version(64), version(255), version(0x0314)}}

	// Pola dodatkowe.
	extra := func(x []byte) []byte { return resized(func(h *testkit.CentralHeader) { h.SetExtra(x) }) }
	exposeExtra := func(key string, ids ...uint16) {
		var cs inventoryCase
		for _, id := range ids {
			cs.archives = append(cs.archives, extra(testkit.Extra(id, []byte{0xDE, 0xAD, 0xBE, 0xEF})))
		}
		cs.check = func(r *Reader) string {
			x := r.Entries()[0].Extra
			if len(x) != 8 || !bytes.Equal(x[4:], []byte{0xDE, 0xAD, 0xBE, 0xEF}) {
				return "the extra field is not exposed as stored"
			}
			return ""
		}
		c[key] = cs
	}
	for _, id := range []uint16{
		// APPNOTE 4.5.2
		0x0007, 0x0008, 0x0009, 0x000a, 0x000c, 0x000d, 0x000e, 0x0014, 0x0015, 0x0016, 0x0018, 0x0019, 0x0020, 0x0021,
		0x0022, 0x0023, 0x0065, 0x0066, 0x4690,
		// APPNOTE 4.6.1
		0x07c8, 0x1986, 0x2605, 0x2705, 0x2805, 0x334d, 0x4154, 0x4341, 0x4453, 0x4704, 0x470f, 0x4854, 0x4b46, 0x4c41,
		0x4d49, 0x4d63, 0x4f4c, 0x5356, 0x5455, 0x554e, 0x5855, 0x6375, 0x6542, 0x6854, 0x7441, 0x756e, 0x7855, 0x7875,
		0xa11e, 0xa220, 0xcafe, 0xd935, 0xe57a, 0xfd4a, 0x9902,
	} {
		exposeExtra(fmt.Sprintf("extra.0x%04x", id), id)
	}
	exposeExtra("extra.unknown", 0x0000, 0x0002, 0x8000, 0xffff)
	c["extra.0x000f"] = one(extra(testkit.Extra(format.ExtraPatchDescriptor, make([]byte, 14))))
	c["extra.0x0017"] = one(extra(testkit.Extra(format.ExtraStrongEncryption, make([]byte, 8))))
	c["extra.0x9901"] = one(extra(testkit.Extra(format.ExtraAEx, make([]byte, 7))))
	c["extra.0x0001"] = inventoryCase{
		archives: [][]byte{testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("hello"), Zip64: true}).Bytes()},
		check: entryCheck(func(e Entry) bool {
			return e.UncompressedSize == 5 && e.CompressedSize == 5 && e.LocalHeaderOffset == 0
		}),
	}
	c["extra.0x0001.unneeded"] = inventoryCase{archives: [][]byte{
		extra(testkit.Extra(format.ExtraZip64, testkit.U64(5))),
		extra(testkit.Extra(format.ExtraZip64)),
	}}
	c["extra.0x0001.missing"] = inventoryCase{archives: [][]byte{
		header(func(h *testkit.CentralHeader) { h.UncompressedSize = format.Sentinel32 }),
		header(func(h *testkit.CentralHeader) { h.CompressedSize = format.Sentinel32 }),
		header(func(h *testkit.CentralHeader) { h.LocalOffset = format.Sentinel32 }),
		header(func(h *testkit.CentralHeader) { h.DiskStart = format.Sentinel16 }),
	}}
	c["extra.0x0001.length"] = inventoryCase{archives: [][]byte{
		resized(func(h *testkit.CentralHeader) { // odsyła jedno pole, a blok ma dwa i drugie przeczy nagłówkowi
			h.UncompressedSize = format.Sentinel32
			h.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(5), testkit.U64(6)))
		}),
		resized(func(h *testkit.CentralHeader) { // długość, która nie jest całą liczbą pól
			h.UncompressedSize = format.Sentinel32
			h.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(5), []byte{0, 0, 0}))
		}),
		resized(func(h *testkit.CentralHeader) { // odsyłają dwa pola, a blok ma jedno
			h.UncompressedSize, h.CompressedSize = format.Sentinel32, format.Sentinel32
			h.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(5)))
		}),
	}}
	c["extra.0x0001.duplicate"] = one(resized(func(h *testkit.CentralHeader) {
		h.UncompressedSize = format.Sentinel32
		h.SetExtra(append(testkit.Extra(format.ExtraZip64, testkit.U64(5)), testkit.Extra(format.ExtraZip64, testkit.U64(5))...))
	}))
	c["extra.0x0001.disk"] = one(resized(func(h *testkit.CentralHeader) {
		h.DiskStart = format.Sentinel16
		h.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U32(1)))
	}))
	c["extra.0x0001.full-form"] = inventoryCase{
		archives: [][]byte{resized(func(h *testkit.CentralHeader) {
			offset := uint64(h.LocalOffset)
			h.UncompressedSize, h.CompressedSize = format.Sentinel32, format.Sentinel32
			h.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(5), testkit.U64(5), testkit.U64(offset)))
		})},
		check: entryCheck(func(e Entry) bool {
			return e.UncompressedSize == 5 && e.CompressedSize == 5 && e.LocalHeaderOffset == 0
		}),
	}
	c["extra.0x7075"] = inventoryCase{
		archives: [][]byte{extra(testkit.UnicodePath("a.txt", []byte("a.txt")))},
		check:    entryCheck(func(e Entry) bool { return e.Name == "a.txt" }),
	}
	c["extra.0x7075.mismatch"] = inventoryCase{archives: [][]byte{
		extra(testkit.UnicodePath("b.txt", []byte("a.txt"))),                                                        // inna nazwa
		extra(testkit.UnicodePath("a.txt", []byte("b.txt"))),                                                        // CRC-32 innego pola nazwy
		extra(testkit.Extra(format.ExtraUnicodePath, []byte{2}, testkit.UnicodePath("a.txt", []byte("a.txt"))[5:])), // wersja 2
		extra(testkit.Extra(format.ExtraUnicodePath, []byte{1, 0})),                                                 // za krótkie
		extra(append(testkit.UnicodePath("a.txt", []byte("a.txt")), testkit.UnicodePath("a.txt", []byte("a.txt"))...)),
	}}
	for k, v := range layoutCases() {
		c[k] = v
	}
	return c
}

var inventoryRow = regexp.MustCompile("^\\| `([a-z0-9]+\\.[a-z0-9.x-]+)` \\| ([^|]+) \\| ([^|]+) \\| `([a-z]+)(?: ([a-z0-9-]+))?` \\| ([0-9]+) \\|$")

// TestFormatInventory mierzy kryterium K1: każdy wiersz inwentarza formatu (requirements/format.md) z etapu już
// wykonanego ma archiwum z daną cechą, a czytnik daje zapisany werdykt. Wiersz bez przypadku, przypadek bez
// wiersza i wiersz o nieznanym werdykcie są błędami.
// REQ: FMT-001 FMT-002 FMT-003 FMT-004 FMT-005 FMT-006 FMT-007
func TestFormatInventory(t *testing.T) {
	doc, err := os.ReadFile("../requirements/format.md")
	if err != nil {
		t.Fatal(err)
	}
	cases := inventoryCases()
	seen := map[string]bool{}
	rows, verified, pending := 0, 0, 0
	for _, line := range strings.Split(strings.ReplaceAll(string(doc), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "| `") || !strings.Contains(line, ".") {
			continue
		}
		m := inventoryRow.FindStringSubmatch(line)
		if m == nil {
			if strings.Count(line, "|") > 3 { // wiersz legendy werdyktów ma dwie kolumny
				t.Errorf("inventory row is not in the form | `key` | section | description | `verdict` | stage |: %s", line)
			}
			continue
		}
		key, kind, code, stage := m[1], m[4], m[5], m[6]
		rows++
		if seen[key] {
			t.Errorf("%s: listed twice", key)
		}
		seen[key] = true
		if kind != "accept" && kind != "expose" && kind != "reject" && kind != "entry" ||
			(kind == "reject" || kind == "entry") != (code != "") {
			t.Errorf("%s: unknown verdict %q %q", key, kind, code)
			continue
		}
		cs, ok := cases[key]
		if stage > fmt.Sprint(inventoryStage) {
			pending++
			if ok {
				t.Errorf("%s: has a test case, so its stage in format.md should be %d", key, inventoryStage)
			}
			continue
		}
		if !ok || len(cs.archives) == 0 {
			t.Errorf("%s: no archive shows this row", key)
			continue
		}
		if kind == "expose" && cs.check == nil {
			t.Errorf("%s: an expose verdict needs a check that the value is handed to the caller", key)
			continue
		}
		good := true
		for i, data := range cs.archives {
			// Wiersze etapu 1 dotyczą katalogu: ich archiwa mają zmieniony tylko katalog (openDir).
			r, err := newReader(bytes.NewReader(data), int64(len(data)), Options{}, stage == "2")
			got := codeOf(err)
			switch kind {
			case "reject":
				if got != code {
					t.Errorf("%s[%d]: got %q, the inventory says reject %s", key, i, got, code)
					good = false
				}
				continue
			case "entry":
				if err != nil || len(r.Entries()) != 1 || r.Entries()[0].Unsupported != code {
					t.Errorf("%s[%d]: got error %q and entries %+v, the inventory says entry %s", key, i, got, entriesOf(r), code)
					good = false
				}
				continue
			}
			if err != nil {
				t.Errorf("%s[%d]: got %v, the inventory says %s", key, i, err, kind)
				good = false
				continue
			}
			for _, e := range r.Entries() {
				if e.Unsupported != "" {
					t.Errorf("%s[%d]: entry marked %s, the inventory says %s", key, i, e.Unsupported, kind)
					good = false
				}
			}
			if cs.check != nil {
				if msg := cs.check(r); msg != "" {
					t.Errorf("%s[%d]: %s", key, i, msg)
					good = false
				}
			}
		}
		if good {
			verified++
		}
	}
	var extra []string
	for key := range cases {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("test cases without a row in requirements/format.md: %s", strings.Join(extra, ", "))
	}
	if rows < 150 {
		t.Errorf("only %d inventory rows found: the tables of requirements/format.md were not read", rows)
	}
	t.Logf("format inventory: %d rows, %d verified, %d pending", rows, verified, pending)
}

func entriesOf(r *Reader) []Entry {
	if r == nil {
		return nil
	}
	return r.Entries()
}
