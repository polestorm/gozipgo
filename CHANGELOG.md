# Changelog

Versions follow semantic versioning. In 0.x the API may grow; an incompatible change needs a decision record in
`docs/adr/`. Release tags are signed from v0.1.0 on.

## Unreleased

Stage 4 of the plan: the writer (`docs/adr/0005-writer-and-compressor.md`).

- New package `zipwriter`: `New`, `Writer.Add` (whole content, sizes in the local header), `Writer.Create`
  (content of unknown length, data descriptor), `Writer.AddRaw` (copies data that are already compressed, for
  example from `zipreader.Reader.OpenRaw`, and checks them on the way), `Writer.Close`. Names follow the rules
  of the reader and are refused with the reader's codes; other errors have `write-*` codes (`docs/errors.md`).
  ZIP64 is written where a size, an offset or the number of entries requires it, or where `Header.Zip64` asks
  for it.
- DEFLATE is compressed by the module's own compressor (`internal/deflate`), written from RFC 1951. It has one
  algorithm with constant parameters: the same content gives the same bytes on every system and with every
  version of Go, whatever the way the content is split into writes. Checksums of its output are kept in the
  repository and compared on every system of the CI and with two versions of Go.
- `tools/compare` also gives the archives `zipwriter` writes to the other readers, and `tools/bench` measures
  writing against `archive/zip`.
- The reader is unchanged; its rules for names moved to a package shared with the writer.

Stage 3 of the plan: hardening and measurements.

- Faster reading: overlapping matches are copied in growing chunks (content made of one repeated byte was nine
  times slower than in `archive/zip`, now 1.3 times), and `Close` of an entry reader lets its DEFLATE decoder be
  reused, which matters for archives of many small entries. Opening an archive of many entries allocates a
  quarter of what it did.
- Hostile corpus (`testdata/hostile.json`, 39 archives built by the tests), `tools/compare` and `tools/bench`
  with their raw results in `docs/results/`, and `docs/comparison.md` written by the tools.
- No change to the API or to which archives are accepted.

## v0.1.0 (2026-11-27)

First release: the reader. Stages 0 to 2 of the plan (`docs/roadmap.md`). Not yet done: the corpus of hostile
archives, the comparison with other programs, measured speed and memory, the 24-hour fuzzing campaign (stage 3).

Since the first commit:

- `zipreader.New` now also reads every local file header and data descriptor and checks the layout of the file:
  entries must not overlap, and no byte before the central directory may be left unreferenced. Archives that
  were accepted on the strength of their directory alone can now be rejected (`zip-local-header-mismatch`,
  `zip-descriptor-mismatch`, `zip-entries-overlap`, `zip-unreferenced-data`, `zip-size-mismatch`).
- `Reader.Open` gives the content of an entry (Stored and Deflate) and checks its size and CRC-32 while it is
  read; `Reader.OpenRaw` gives the stored bytes. New errors: `zip-checksum`, `inflate-*`, `limit-entry-size`,
  `limit-total-size`, `limit-ratio`, and the values `ErrNoEntry` and `ErrClosed`.
- DEFLATE is decoded by the module's own decoder, written from RFC 1951; `compress/flate` is not imported.
- New options: `MaxEntrySize`, `MaxTotalSize`, `MaxRatio`, `AllowPrefix`, and `UnflaggedNames: CodePage` with
  `CodePage` for names in a code page the caller supplies.
- New field `Entry.DataOffset`. `Entry.LocalHeaderOffset` is now documented as a position in the file.
- The module requires the current Go release (1.26).

Decisions: `docs/adr/0002-directory-reading-rules.md`, `docs/adr/0003-local-headers-layout-and-content.md`.
