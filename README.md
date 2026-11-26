# gozipgo

Reads ZIP archives in pure Go, written from the ZIP file format specification, for input that is not trusted.
No cgo, no dependencies outside the Go standard library, and no use of `archive/zip`.

Status: in development. Stages 1 and 2 of the [plan](docs/roadmap.md) are done: the reader checks the end of
central directory records, ZIP64, the central directory, every local header and data descriptor and the layout of
the file, and reads entry contents (Stored and Deflate, with its own DEFLATE decoder) while checking size and
CRC-32. It rejects every archive of its hostile corpus, and reading is about as fast as `archive/zip`
([comparison](docs/comparison.md), [method](docs/methodology.md)). Still to come before 1.0: a 24-hour fuzzing
campaign and the trial at the first consumer. A writer with byte-identical output follows.

Use:

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
- **the same result every time**, on every platform and with every Go version.

Not in scope: encryption, multi-volume archives, compression methods other than Stored and Deflate, repairing
damaged archives, extracting to disk.

What other programs do with the same hostile archives, and measured speed: [docs/comparison.md](docs/comparison.md).

Error codes: [docs/errors.md](docs/errors.md). What the reader rejects that other programs open, and why:
[docs/differences.md](docs/differences.md).

Licence: Apache 2.0.
