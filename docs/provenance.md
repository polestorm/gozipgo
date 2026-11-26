# Code provenance

`gozipgo` is written from scratch, from the specifications. This file says what we may use and what we may not.

## Sources we write from

- APPNOTE.TXT, the .ZIP File Format Specification published by PKWARE, pinned below;
- ISO/IEC 21320-1:2015, Document Container File, Part 1: Core, the restricted ZIP profile that OPC, ODF and EPUB
  build on. On 2026-11-26 we could not confirm that ISO offers its text free of charge, so it is only cited: no
  requirement of stages 0 and 1 is taken from it. It refers to APPNOTE 6.3.3 and narrows it, so the pinned
  APPNOTE covers the format;
- RFC 1951, DEFLATE Compressed Data Format Specification version 1.3;
- the definition of CRC-32 given in APPNOTE;
- published descriptions of attacks on ZIP readers (overlapping entries, disagreeing headers, concatenated
  archives), as the source of test cases, cited where used.

## The pinned specification

| | |
|---|---|
| Document | APPNOTE.TXT - .ZIP File Format Specification |
| Version | 6.3.10, status FINAL, revised Nov 01, 2022 |
| Publisher | PKWARE Inc. |
| Fetched from | `https://pkware.cachefly.net/webdocs/casestudies/APPNOTE.TXT` on 2026-11-26 |
| Size | 174,585 bytes (CRLF line ends, as served) |
| SHA-256 | `0b993022a7d320a0bf704e6980bea36fafd17a6066ab994db0a0c16278a50cd6` |

The text is not copied into the repository (PKWARE keeps the copyright; section 1.4 of the document sets the terms
of use). Section numbers in `requirements/`, `docs/errors.md` and the comments of the code refer to this version.
A new version of APPNOTE is adopted by a review of the format inventory (`requirements/format.md`) against its
change log, and this table is then updated.

The table of IBM Code Page 437 used for `UnflaggedNames: CP437` is data, not code: the mapping published by the
Unicode Consortium (`MAPPINGS/VENDORS/MICSFT/PC/CP437.TXT`), checked against the output of the `cp437` codec of
Python 3.9.6 used as a black box; a test keeps its checksum.

## What we do not do

We do not read, copy or port the code of ZIP and DEFLATE implementations: `archive/zip` and `compress/flate` of
the Go standard library, Info-ZIP, zlib, miniz, 7-Zip, libzip, libarchive, Python `zipfile`, `java.util.zip`,
klauspost/compress, or their ports. The list is not closed.

Unlike the other go*go projects, this rule covers the Go standard library too: here `archive/zip` and
`compress/flate` are the alternative we compare with and the oracle in our tests, so they cannot also be the model.

The reason is not the licence but three goals: code that is entirely ours and easy to show to be so, an evidence
package that ties the code to the rules of the specification, and the specification, not someone else's
implementation, as the reference.

## What we may do

- call `archive/zip`, `compress/flate`, Info-ZIP, 7-Zip and Python `zipfile` as black boxes, in tests and in
  `tools/compare`, and compare results;
- read their user documentation;
- use `hash/crc32` and other packages of the standard library without input or output through their public API.

## Test data

Archives made by other programs are test data, not code. Sources, licences, the commands that made them and
checksums: [`../testdata/README.md`](../testdata/README.md).
