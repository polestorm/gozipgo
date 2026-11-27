# Changelog

Versions follow semantic versioning. In 0.x the API may grow; an incompatible change needs a decision record in
`docs/adr/`. Release tags are signed from v0.1.0 on.

## v0.1.1 (2026-11-27)

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
