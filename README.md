# gozipgo

Reads ZIP archives in pure Go, written from the ZIP file format specification, for input that is not trusted.
No cgo, no dependencies outside the Go standard library, and no use of `archive/zip`.

Status: in development, not released. Stage 1 of the [plan](docs/roadmap.md) is done: the reader checks the end
of central directory records, ZIP64 and the central directory and lists the entries. It does not yet read local
headers or entry contents (`Open` comes with stage 2 and v0.1.0), so it is not yet fit for hostile input. A writer
with byte-identical output follows.

Planned use:

```go
import "github.com/polestorm/gozipgo/zipreader"

r, err := zipreader.New(f, size, zipreader.Options{}) // io.ReaderAt; default limits; strict
if err != nil {
	return err // *zipreader.Error with Code, Entry, Offset
}
for _, e := range r.Entries() { // central directory order
	rc, err := r.Open(e) // stage 2: size, CRC-32 and limits are checked while reading
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
  length, all counted on the real data, not on header fields;
- **no file system, no network**: the library reads from an `io.ReaderAt` and gives back readers; it never
  extracts to disk;
- **the same result every time**, on every platform and with every Go version.

Not in scope: encryption, multi-volume archives, compression methods other than Stored and Deflate, repairing
damaged archives, extracting to disk.

Error codes: [docs/errors.md](docs/errors.md). What the reader rejects that other programs open, and why:
[docs/differences.md](docs/differences.md).

Licence: Apache 2.0.
