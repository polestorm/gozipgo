package main

import (
	"bytes"
	"fmt"
	"io"

	"github.com/polestorm/gozipgo/zipreader"
	"github.com/polestorm/gozipgo/zipwriter"
)

// written is an archive made by zipwriter from data this program generates, with the content every reader
// must give back.
type written struct {
	ID    string
	What  string
	Data  []byte
	Files map[string]string // name → SHA-256 of the content, directories left out
}

// generate returns n bytes of deterministic data of the named kind.
func generate(kind string, n int) []byte {
	b := make([]byte, n)
	x := uint32(len(kind))*2654435761 + 4321
	next := func() uint32 { x = x*1664525 + 1013904223; return x >> 8 }
	switch kind {
	case "xml":
		words := []string{"<w:p>", "</w:p>", "<w:r>", "</w:r>", "<w:t>", "</w:t>", "<w:rPr>", "<w:b/>", "</w:rPr>",
			"Lorem", "ipsum", "dolor", "sit", "amet", "zażółć", "gęślą", "jaźń", " ", " ", " ", "\n", "0", "1", "2024"}
		for i := 0; i < n; {
			i += copy(b[i:], words[next()%uint32(len(words))])
		}
	case "random":
		for i := range b {
			b[i] = byte(next())
		}
	case "zeros":
	}
	return b
}

// builder collects the calls on one Writer and the content expected back.
type builder struct {
	out   bytes.Buffer
	w     *zipwriter.Writer
	files map[string]string
}

func newBuilder() *builder {
	b := &builder{files: map[string]string{}}
	b.w = zipwriter.New(&b.out)
	return b
}

func (b *builder) expect(name string, content []byte) {
	if name[len(name)-1] != '/' {
		b.files[name] = sum(content)
	}
}

func (b *builder) add(h zipwriter.Header, content []byte) {
	if err := b.w.Add(h, content); err != nil {
		fatal("writing an archive:", err)
	}
	b.expect(h.Name, content)
}

func (b *builder) stream(h zipwriter.Header, content []byte) {
	e, err := b.w.Create(h)
	if err == nil {
		for len(content) > 0 && err == nil {
			n := min(len(content), 10000)
			_, err = e.Write(content[:n])
			content = content[n:]
		}
	}
	if err == nil {
		err = e.Close()
	}
	if err != nil {
		fatal("writing an archive:", err)
	}
	b.expect(h.Name, nil)
}

func (b *builder) done(id, what string) written {
	if err := b.w.Close(); err != nil {
		fatal("writing an archive:", err)
	}
	return written{ID: id, What: what, Data: b.out.Bytes(), Files: b.files}
}

// writtenArchives builds the archives of the third corpus: every way of writing an entry, both methods, every
// form of the headers the writer can produce short of sizes above 4 GiB.
func writtenArchives() []written {
	xml, random, zeros := generate("xml", 400000), generate("random", 150000), generate("zeros", 3<<20)
	stored, deflate := zipwriter.Stored, zipwriter.Deflate
	var all []written

	b := newBuilder()
	b.add(zipwriter.Header{Name: "mimetype", Method: stored}, []byte("application/vnd.openxmlformats-officedocument.wordprocessingml.document"))
	b.add(zipwriter.Header{Name: "[Content_Types].xml", Method: deflate}, xml[:3000])
	b.add(zipwriter.Header{Name: "_rels/.rels", Method: deflate}, xml[3000:4000])
	b.add(zipwriter.Header{Name: "word/", Method: stored}, nil)
	b.add(zipwriter.Header{Name: "word/document.xml", Method: deflate, Modified: zipwriter.Timestamp{Year: 2026, Month: 11, Day: 27, Hour: 12, Minute: 30}}, xml)
	b.add(zipwriter.Header{Name: "word/media/image1.bin", Method: deflate}, random)
	b.add(zipwriter.Header{Name: "word/media/image2.bin", Method: stored}, random[:50000])
	b.add(zipwriter.Header{Name: "word/empty.xml", Method: deflate}, nil)
	all = append(all, b.done("added", "entries written by `Add`: sizes in the local headers, Stored and Deflate, a directory, an empty file"))

	b = newBuilder()
	for _, e := range []struct {
		name    string
		method  uint16
		content []byte
	}{{"a/markup.xml", deflate, xml}, {"a/noise.bin", deflate, random}, {"a/stored.bin", stored, random[:70000]},
		{"a/zeros.bin", deflate, zeros}, {"a/empty", deflate, nil}, {"a/empty-stored", stored, nil}, {"b/", stored, nil}} {
		b.stream(zipwriter.Header{Name: e.name, Method: e.method}, e.content)
		b.expect(e.name, e.content)
	}
	all = append(all, b.done("streamed", "entries written by `Create`: data descriptors, Stored and Deflate, 3 MiB of zeros, empty files"))

	b = newBuilder()
	b.add(zipwriter.Header{Name: "zażółć gęślą jaźń.txt", Method: deflate}, []byte("pchnąć w tę łódź jeża lub ośm skrzyń fig"))
	b.add(zipwriter.Header{Name: "katalog żółty/", Method: stored}, nil)
	b.add(zipwriter.Header{Name: "katalog żółty/plik ś.xml", Method: deflate}, xml[:20000])
	b.stream(zipwriter.Header{Name: "日本語/ファイル.bin", Method: stored}, random[:1000])
	b.expect("日本語/ファイル.bin", random[:1000])
	all = append(all, b.done("utf8-names", "names outside ASCII: UTF-8 with bit 11"))

	b = newBuilder()
	b.add(zipwriter.Header{Name: "added.xml", Method: deflate, Zip64: true}, xml[:100000])
	b.stream(zipwriter.Header{Name: "streamed.xml", Method: deflate, Zip64: true}, xml[:100000])
	b.expect("streamed.xml", xml[:100000])
	b.stream(zipwriter.Header{Name: "streamed-stored.bin", Method: stored, Zip64: true}, random[:30000])
	b.expect("streamed-stored.bin", random[:30000])
	b.add(zipwriter.Header{Name: "plain.txt", Method: stored}, []byte("an entry without ZIP64"))
	all = append(all, b.done("zip64-headers", "`Header.Zip64`: local headers with the ZIP64 extra field, 64-bit data descriptors"))

	b = newBuilder()
	for i := 0; i < 66000; i++ {
		// the content names the entry, so every file is different and a reader cannot merge two
		b.add(zipwriter.Header{Name: fmt.Sprintf("%03d/%05d.txt", i/500, i), Method: stored}, []byte(fmt.Sprintf("entry %d\n", i)))
	}
	all = append(all, b.done("zip64-count", "66,000 entries: ZIP64 end of central directory record and locator"))

	// Copy the first archive entry by entry without compressing again.
	src := all[0]
	r, err := zipreader.New(bytes.NewReader(src.Data), int64(len(src.Data)), zipreader.Options{})
	if err != nil {
		fatal("reading back:", err)
	}
	b = newBuilder()
	for _, e := range r.Entries() {
		raw, err := r.OpenRaw(e)
		if err != nil {
			fatal(err)
		}
		err = b.w.AddRaw(zipwriter.Header{Name: "copy/" + e.Name, Method: e.Method}, raw,
			zipwriter.RawInfo{CompressedSize: e.CompressedSize, UncompressedSize: e.UncompressedSize, CRC32: e.CRC32})
		if err != nil {
			fatal("copying an entry:", err)
		}
		if !e.IsDir {
			rc, err := r.Open(e)
			if err != nil {
				fatal(err)
			}
			content, err := io.ReadAll(rc)
			if err != nil {
				fatal(err)
			}
			b.expect("copy/"+e.Name, content)
		}
	}
	all = append(all, b.done("raw-copy", "the entries of `added` copied by `AddRaw` from `OpenRaw`, not compressed again"))

	b = newBuilder()
	all = append(all, b.done("empty", "an archive with no entries: an end of central directory record and nothing else"))
	return all
}
