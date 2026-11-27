# gozipgo

Reads and writes ZIP archives in pure Go, written from the ZIP file format specification: a reader for input
that is not trusted, and a writer whose output is the same bytes every time. No cgo, no dependencies outside the
Go standard library, and no use of `archive/zip` or `compress/flate`.

Status: in development. Stages 1 to 4 of the [plan](docs/roadmap.md) are done. The reader checks the end of
central directory records, ZIP64, the central directory, every local header and data descriptor and the layout of
the file, and reads entry contents (Stored and Deflate, with its own DEFLATE decoder) while checking size and
CRC-32. It rejects every archive of its hostile corpus, and reading is about as fast as `archive/zip`. The writer
has its own DEFLATE compressor; what it writes the reader accepts in strict mode, and 7-Zip, Python, bsdtar and
`archive/zip` read it back unchanged ([comparison](docs/comparison.md), [method](docs/methodology.md)). Still to
come before 1.0: a 24-hour fuzzing campaign and the trial at the first consumer.

Reading:

```go
import "github.com/polestorm/gozipgo/zipreader"

r, err := zipreader.New(f, size, zipreader.Options{}) // io.ReaderAt; default limits; strict
if err != nil {
	return err // *zipreader.Error with Code, Entry, Offset
}
for _, e := range r.Entries() { // central directory order
	rc, err := r.Open(e) // size, CRC-32 and limits are checked while reading
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, rc)
	rc.Close()
	if err != nil {
		return err
	}
}
```

Writing:

```go
import "github.com/polestorm/gozipgo/zipwriter"

w := zipwriter.New(out) // io.Writer; never seeks, never reads the clock
err := w.Add(zipwriter.Header{Name: "word/document.xml", Method: zipwriter.Deflate}, content)
if err != nil {
	return err // *zipwriter.Error with Code, Entry, Name; names the reader would reject are refused
}
entry, err := w.Create(zipwriter.Header{Name: "big.bin", Method: zipwriter.Deflate}) // content of unknown length
// ... entry.Write(...) any number of times, then entry.Close()
err = w.Close() // central directory and end records
```

The same calls give the same bytes on every system and with every Go version: the compressor has one fixed
algorithm, the date of an entry is the one in its header (1980-01-01 when none is given), and nothing else varies.
`AddRaw` copies an entry from `zipreader.Reader.OpenRaw` without compressing it again, checking it on the way.

What it is for:

- **archives from outside**: office documents (DOCX, XLSX are ZIP packages), uploaded bundles, anything where the
  file may have been built to confuse the reader;
- **one reading or none**: a ZIP archive describes each file twice (central directory and local header) and can
  hold data no directory entry points to. Different programs resolve the disagreements differently, which is how
  a file can show one content to a scanner and another to the user. gozipgo accepts an archive only when all of
  its structures agree, and rejects it with a fixed error code otherwise;
- **limits on by default**: number of entries, total and per-entry uncompressed size, compression ratio, name
  length; reading never delivers more than an entry declares, so header fields cannot be used to get around them;
- **no file system, no network**: the library reads from an `io.ReaderAt` and gives back readers; it never
  extracts to disk;
- **the same result every time**, on every platform and with every Go version, for reading and for the bytes
  the writer produces.

Not in scope: encryption, multi-volume archives, compression methods other than Stored and Deflate, repairing
damaged archives, extracting to disk.

What other programs do with the same hostile archives and with the archives gozipgo writes, and measured speed
and size: [docs/comparison.md](docs/comparison.md).

Error codes: [docs/errors.md](docs/errors.md). What the reader rejects that other programs open, and why:
[docs/differences.md](docs/differences.md).

Licence: Apache 2.0.
