# Changelog

Versions follow semantic versioning. In 0.x the API may grow; an incompatible change needs a decision record in
`docs/adr/`. Release tags are signed from v0.1.0 on.

## Unreleased

Includes the security fix released as v0.1.1 (below).

Two new options of the reader, both off by default and reported by `Reader.Relaxed`
(`docs/adr/0006-relaxations-from-the-first-consumer.md`): `AllowVersionHostByte` takes only the lower byte of
"version needed to extract", for archives whose writer put the host system in the upper byte, and
`AllowUnusedZip64` accepts a local header with a ZIP64 extra field of zeros next to real sizes. Strict reading is
unchanged.

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
  repository and compared on every system of the CI and with two versions of Go. Writing is not slower than
  with `archive/zip` and the archives are about the same size (`docs/comparison.md`).
- The compressor was rewritten before its first release
  (`docs/adr/0007-faster-compressor-and-self-check.md`): matches come from two hash tables instead of hash
  chains, and the main loop handles one position per turn with its state in local variables. Compression is
  several times faster than in the first version, with output of about the same size (`docs/comparison.md`).
  The bytes the writer produces changed with it, and the recorded checksums were replaced; no release contained
  the earlier bytes. A compressor uses about 1.8 MiB of memory (0.5 MiB before).
- The writer checks its own compressor: every DEFLATE stream is decoded while it is written, and the size and
  the CRC-32 of the result are compared with the content of the entry. A difference is the new error
  `write-self-check` (`CodeSelfCheck`) instead of an archive that unpacks to something else.
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

## v0.1.1 (2026-11-27)

Released from the branch `release-0.1`: v0.1.0 and this fix only.

Security fix in the reader, found by `FuzzReader`: an archive whose comment holds the signature of an end of
central directory record was accepted even when the record starting there did not end with the file.
`archive/zip`, Python `zipfile`, UnZip and bsdtar start from the last signature without that check, so a second
archive followed by one more byte in the comment of the first made them read other files than gozipgo did. Such an
archive is now rejected with `zip-multiple-directory-ends`; so is an end record with the signature inside its own
fields. The set of archives accepted in strict mode narrows by exactly these
(`docs/adr/0002-directory-reading-rules.md`, point 3). No change to the API.

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
