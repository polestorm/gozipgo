package zipwriter_test

import (
	"bytes"
	"fmt"
	"io"

	"github.com/polestorm/gozipgo/zipreader"
	"github.com/polestorm/gozipgo/zipwriter"
)

// Write an archive to memory and read it back.
func ExampleWriter() {
	var out bytes.Buffer
	w := zipwriter.New(&out)

	// The whole content at once: sizes and CRC-32 go to the local header.
	err := w.Add(zipwriter.Header{Name: "mimetype"}, []byte("application/zip"))
	if err != nil {
		fmt.Println(err)
		return
	}

	// Content of unknown length: write it in pieces, then close the entry.
	entry, err := w.Create(zipwriter.Header{
		Name:     "docs/readme.txt",
		Method:   zipwriter.Deflate,
		Modified: zipwriter.Timestamp{Year: 2026, Month: 11, Day: 27, Hour: 12},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	for i := 0; i < 100; i++ {
		fmt.Fprintln(entry, "the same line, a hundred times")
	}
	if err := entry.Close(); err != nil {
		fmt.Println(err)
		return
	}

	// A name the reader would reject is refused; the writer stays usable.
	err = w.Add(zipwriter.Header{Name: "../outside"}, nil)
	fmt.Println(err)

	if err := w.Close(); err != nil {
		fmt.Println(err)
		return
	}

	r, err := zipreader.New(bytes.NewReader(out.Bytes()), int64(out.Len()), zipreader.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range r.Entries() {
		rc, err := r.Open(e)
		if err != nil {
			fmt.Println(err)
			return
		}
		n, err := io.Copy(io.Discard, rc)
		rc.Close()
		fmt.Println(e.Name, n, e.CompressedSize, err)
	}
	// Output:
	// zip: name-traversal (entry 2 "../outside"): an empty, "." or ".." path segment in the name
	// mimetype 15 15 <nil>
	// docs/readme.txt 3100 53 <nil>
}

// Copy entries from one archive to another without compressing them again.
func ExampleWriter_AddRaw() {
	var first bytes.Buffer
	w := zipwriter.New(&first)
	w.Add(zipwriter.Header{Name: "a.txt", Method: zipwriter.Deflate}, bytes.Repeat([]byte("abc"), 1000))
	w.Add(zipwriter.Header{Name: "b.txt"}, []byte("kept as it is"))
	w.Close()

	r, err := zipreader.New(bytes.NewReader(first.Bytes()), int64(first.Len()), zipreader.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	var second bytes.Buffer
	w = zipwriter.New(&second)
	for _, e := range r.Entries() {
		raw, err := r.OpenRaw(e)
		if err != nil {
			fmt.Println(err)
			return
		}
		// AddRaw checks the data against the sizes and the CRC-32 while it copies them.
		err = w.AddRaw(zipwriter.Header{Name: "copy/" + e.Name, Method: e.Method}, raw, zipwriter.RawInfo{
			CompressedSize: e.CompressedSize, UncompressedSize: e.UncompressedSize, CRC32: e.CRC32,
		})
		if err != nil {
			fmt.Println(err)
			return
		}
	}
	fmt.Println(w.Close())
	// Output:
	// <nil>
}
