# Comparison

What gozipgo and other ZIP readers do with the same archives, and how fast gozipgo reads against `archive/zip`.
Every table is written by a tool of this repository; the input, the versions, the commands and what each word in
a cell means are in [methodology.md](methodology.md), and the raw output is in [results/](results/). To repeat:

```sh
go -C tools/compare run . -root ../..     # the two tables below; raw: docs/results/compare.tsv
go -C tools/bench run . -root ../..       # speed and memory; raw: docs/results/bench.tsv
```

## Behaviour

<!-- compare:begin -->
Measured on 2026-11-27 at commit `0a48ee9` on darwin/arm64, Apple M4, macOS 27.0.1. Readers:

- gozipgo: this commit
- archive/zip: go1.26.6
- Info-ZIP unzip: UnZip 6.00 of 20 April 2009, by Info-ZIP, with modifications by Apple Inc.
- 7-Zip: 7-Zip (z) 26.01 (arm64) : Copyright (c) 1999-2026 Igor Pavlov : 2026-04-27
- Python zipfile: Python 3.9.6 (default, Aug 25 2026, 21:26:21)
- bsdtar (libarchive): bsdtar 3.5.3 - libarchive 3.7.4 zlib/1.2.12 liblzma/5.4.3 bz2lib/1.0.8

### Conformance corpus

25 archives made by real programs (`testdata/interop`). "same" means the reader gives exactly the files
the manifest records (SHA-256 of every file; for readers that list names, the names too, where the archive stores
them as UTF-8 or ASCII).

| Archive | gozipgo | archive/zip | Info-ZIP unzip | 7-Zip | Python zipfile | bsdtar (libarchive) |
|---|---|---|---|---|---|---|
| `7zip-default.zip` | same | same | same | same | same | same |
| `7zip-fastest.zip` | same | same | same | same | same | same |
| `7zip-stored.zip` | same | same | same | same | same | same |
| `7zip-ultra.zip` | same | same | same | same | same | same |
| `go-deflate.zip` | same | same | rejects: exit status 50, 7 files written | same | same | same |
| `go-raw.zip` | same | same | rejects: exit status 50, 7 files written | same | same | same |
| `go-stored.zip` | same | same | rejects: exit status 50, 7 files written | same | same | same |
| `infozip-best.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `infozip-comment.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `infozip-default.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `infozip-noextra.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `infozip-stored.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `infozip-stream.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `infozip-zip64.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `libreoffice-docx.docx` | same | same | same | same | same | same |
| `libreoffice-odt.odt` | same | same | same | same | same | same |
| `macos-ditto.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | same |
| `macos-ditto-rsrc.zip` | same with `UnflaggedNames=UTF8` | same | same | same | same | differs (8 files) |
| `python-deflated.zip` | same | same | same | same | same | same |
| `python-stored.zip` | same | same | same | same | same | same |
| `python-stream.zip` | same | same | same | same | same | same |
| `python-zip64.zip` | same | same | same | same | same | same |
| `windows11-7zip.zip` | same with `UnflaggedNames=CodePage:CP852` | same | rejects: exit status 50, 1 files written | same | same | same |
| `windows11-explorer.zip` | same | same | same | same | same | same |
| `word-docx.docx` | same | same | same | same | same | same |
| **same** | **25 / 25** | **25 / 25** | **21 / 25** | **25 / 25** | **25 / 25** | **24 / 25** |

### Hostile corpus

40 archives built by the tests of the repository (`testdata/hostile.json`). A cell says what the reader did:
"rejects", or "reads N" with the number of files it produced and a letter for their content, so two readers with
different letters in one row read different content from the same file. "escapes" means files were written
outside the directory the reader was told to extract into.

| Archive | What it is | gozipgo | archive/zip | Info-ZIP unzip | 7-Zip | Python zipfile | bsdtar (libarchive) |
|---|---|---|---|---|---|---|---|
| `bomb-ratio` | one entry of 16 MiB of zeros compressed 1032:1, the most DEFLATE can do | rejects: `limit-ratio` | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) |
| `bomb-lying-size` | the headers declare 1000 bytes, the DEFLATE stream expands to 16 MiB | rejects: `zip-size-mismatch` | rejects | rejects after writing 1 | rejects after writing 1 | reads 1 (A) | rejects after writing 1 |
| `overlap-shared-stream` | 64 directory entries point at one local header and one DEFLATE stream of 1 MiB | rejects: `zip-entries-overlap` | reads 64 (A) | reads 64 (A), warning | reads 64 (A) | rejects | rejects |
| `overlap-quoted-header` | the stored block of entry A quotes the local header of entry B, and both entries continue into the same DEFLATE stream | rejects: `zip-entries-overlap` | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) | rejects |
| `entries-over-limit` | 65,537 empty entries, one more than the default limit | rejects: `limit-entries` | reads 65537 (A) | reads 65537 (A) | reads 65537 (A) | reads 65537 (A) | reads 65537 (A) |
| `entries-count-lie` | the end record declares 60,000 entries in a directory of 102 bytes | rejects: `zip-directory-count-mismatch` | rejects | rejects after writing 2 | rejects after writing 2 | reads 2 (A) | reads 2 (A) |
| `directory-past-file` | the central directory offset points past the end of the file | rejects: `zip-directory-out-of-bounds` | rejects | rejects after writing 2 | rejects after writing 2 | rejects | reads 2 (A) |
| `directory-over-data` | the central directory is declared to start inside the data of the first entry | rejects: `zip-directory-signature` | rejects | rejects | rejects after writing 2 | rejects | rejects |
| `two-ends-comment` | a whole second archive in the comment of the first | rejects: `zip-multiple-directory-ends` | reads 1 (A) | reads 1 (A), warning | reads 2 (B) | reads 1 (A) | reads 1 (A) |
| `two-ends-comment-trailing` | a second archive and one more byte in the comment of the first: the inner end record does not end with the file | rejects: `zip-multiple-directory-ends` | reads 1 (A) | reads 1 (A), warning | reads 2 (B) | reads 1 (A) | reads 1 (A) |
| `concatenated` | two archives one after the other | rejects: `zip-directory-out-of-bounds` | reads 1 (A) | reads 1 (A), warning | reads 2 (B) | reads 1 (A) | reads 1 (A) |
| `data-after-end` | bytes after the comment of the end of central directory record | rejects: `zip-trailing-data` | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) |
| `local-name` | the local header names the entry e.exe, the directory a.txt | rejects: `zip-local-header-mismatch` | reads 2 (A) | reads 2 (A), warning | reads 2 (A) | rejects | reads 2 (A) |
| `local-method` | the local header says Deflate, the directory says Stored | rejects: `zip-local-header-mismatch` | reads 2 (A) | rejects after writing 2 | rejects after writing 2 | reads 2 (A) | rejects after writing 1 |
| `local-size` | the local header declares 3 bytes, the directory 5 | rejects: `zip-local-header-mismatch` | reads 2 (A) | rejects after writing 2 | rejects after writing 1 | reads 2 (A) | rejects after writing 2 |
| `descriptor-size` | the data descriptor declares another size than the directory | rejects: `zip-descriptor-mismatch` | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) |
| `hidden-entry` | a complete entry between two listed ones that the directory does not mention | rejects: `zip-unreferenced-data` | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) |
| `prefix` | a program before the first entry (self-extracting layout) | rejects: `zip-unreferenced-data` | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) | reads 2 (A) |
| `name-traversal` | the name ../../outside.txt | rejects: `name-traversal` | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) | rejects |
| `name-absolute` | the name /etc/passwd | rejects: `name-absolute` | reads 1 (A) | reads 1 (A), warning | reads 1 (A) | reads 1 (A) | reads 1 (A) |
| `name-drive` | the name C:/Windows/win.ini | rejects: `name-absolute` | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) |
| `name-backslash` | the name ..\..\outside.txt | rejects: `name-backslash` | reads 1 (A) | reads 1 (A) | reads 1 (A) | reads 1 (A) | rejects |
| `name-nul` | the name safe.txt<NUL>.exe | rejects: `name-nul` | reads 1 (A) | reads 1 (A) | rejects | reads 1 (A) | reads 1 (A) |
| `name-duplicate` | two entries named a.txt with different content | rejects: `name-duplicate` | reads 2 (A) | reads 1 (B) | reads 1 (B) | reads 2 (A) | reads 1 (B) |
| `name-file-and-directory` | a file a and an entry a/b | rejects: `name-duplicate` | reads 2 (A) | rejects after writing 1 | rejects after writing 1 | reads 2 (A) | rejects after writing 1 |
| `zip64-size-past-file` | the ZIP64 extra field of the directory declares 1 TiB of data, the local header 5 bytes | rejects: `zip-local-header-mismatch` | rejects | reads 1 (A) | rejects after writing 1 | rejects | rejects after writing 1 |
| `zip64-disagrees` | the ZIP64 end record declares another number of entries than the ordinary one | rejects: `zip64-inconsistent` | reads 1 (A) | rejects after writing 1 | reads 1 (A) | reads 1 (A) | reads 1 (A) |
| `zip64-locator-astray` | the ZIP64 locator points into the data of the first entry | rejects: `zip64-inconsistent` | rejects | reads 1 (A) | rejects after writing 1 | reads 1 (A) | reads 1 (A) |
| `extra-past-header` | an extra field block declares more data than the extra field holds | rejects: `zip-extra-field` | reads 2 (A) | reads 2 (A) | reads 2 (A) | rejects | rejects |
| `extra-length-past-directory` | the extra field length of the last directory header runs past the directory | rejects: `zip-directory-truncated` | rejects | rejects after writing 1 | rejects after writing 2 | reads 2 (A) | rejects |
| `truncated-in-data` | the file ends inside the data of the first entry | rejects: `zip-no-directory-end` | rejects | rejects | rejects | rejects | rejects |
| `truncated-in-directory` | the file ends inside the central directory | rejects: `zip-no-directory-end` | rejects | rejects | rejects after writing 2 | rejects | reads 2 (A) |
| `truncated-in-end` | the file ends inside the end of central directory record | rejects: `zip-no-directory-end` | rejects | rejects | rejects after writing 2 | rejects | reads 2 (A) |
| `inflate-block-type` | a DEFLATE block of the reserved type | rejects: `inflate-block-type` | rejects | rejects after writing 1 | rejects after writing 1 | rejects | rejects after writing 1 |
| `inflate-stored-length` | a stored block whose NLEN is not the complement of LEN | rejects: `inflate-stored-length` | rejects | rejects after writing 1 | rejects after writing 1 | rejects | rejects after writing 1 |
| `inflate-code-lengths` | a dynamic block that declares 287 literal/length codes | rejects: `inflate-code-lengths` | rejects | rejects after writing 1 | rejects after writing 1 | rejects | rejects after writing 1 |
| `inflate-distance` | a match that reaches before the start of the output | rejects: `inflate-distance` | rejects | rejects after writing 1 | rejects after writing 1 | rejects | rejects after writing 1 |
| `inflate-truncated` | the compressed data end inside the stream | rejects: `inflate-truncated` | rejects | rejects after writing 1 | rejects after writing 1 | rejects | rejects after writing 1 |
| `inflate-trailing-data` | bytes after the end of the DEFLATE stream inside the entry | rejects: `inflate-trailing-data` | reads 1 (A) | reads 1 (A) | rejects after writing 1 | reads 1 (A) | rejects after writing 1 |
| `crc` | the content does not have the declared CRC-32 | rejects: `zip-checksum` | rejects | rejects after writing 2 | rejects after writing 2 | rejects | rejects after writing 2 |
| **reads** | | **0 / 40** | **24 / 40** | **22 / 40** | **19 / 40** | **25 / 40** | **20 / 40** |

### Archives written by gozipgo

7 archives written by `zipwriter` from data the tool generates. "same" means the reader gives back exactly
the files that were written (SHA-256 of every file; for readers that list names, the names too). gozipgo reads
them in its strict mode, with its default limits except the one on the number of entries.

| Archive | What it is | gozipgo | archive/zip | Info-ZIP unzip | 7-Zip | Python zipfile | bsdtar (libarchive) |
|---|---|---|---|---|---|---|---|
| `added` | entries written by `Add`: sizes in the local headers, Stored and Deflate, a directory, an empty file | same | same | same | same | same | same |
| `streamed` | entries written by `Create`: data descriptors, Stored and Deflate, 3 MiB of zeros, empty files | same | same | same | same | same | same |
| `utf8-names` | names outside ASCII: UTF-8 with bit 11 | same | same | rejects: exit status 50 | same | same | same |
| `zip64-headers` | `Header.Zip64`: local headers with the ZIP64 extra field, 64-bit data descriptors | same | same | same | same | same | same |
| `zip64-count` | 66,000 entries: ZIP64 end of central directory record and locator | same | same | same | same | same | same |
| `raw-copy` | the entries of `added` copied by `AddRaw` from `OpenRaw`, not compressed again | same | same | same | same | same | same |
| `empty` | an archive with no entries: an end of central directory record and nothing else | same | same | same (with a warning) | same | same | same |
| **same** | | **7 / 7** | **7 / 7** | **6 / 7** | **7 / 7** | **7 / 7** | **7 / 7** |
<!-- compare:end -->

Reading the hostile table: gozipgo rejects every archive of the hostile corpus, each with the code the corpus
manifest names; that is checked by `TestHostileArchives` and again by the tool. The other columns are not a ranking.
Most of these archives are not invalid by the letter of the specification, and a reader that extracts files to
disk has other defences (unzip and bsdtar strip `../`, for example). What the table shows is where one file means
different things to different readers, which is the property gozipgo is built to exclude. The archives gozipgo
rejects although others read them are listed, with the reason, in [differences.md](differences.md).

## Speed and memory

<!-- bench:begin -->
Measured on 2026-11-27 at commit `435fc4e` with go1.26.6 on darwin/arm64, Apple M4, macOS 27.0.1. Median of 7 runs after one warm-up run; a ratio below 1 means
gozipgo is faster. Reading includes opening the archive and, for both readers, checking the CRC-32.
The largest ratio for reading content is 1.31; criterion K7 allows 1.5.

| Case | gozipgo | archive/zip | time, gozipgo / archive/zip | allocated, gozipgo | allocated, archive/zip |
|---|---|---|---|---|---|
| Deflate, xml, 32 MiB (7266999 bytes compressed) | 524 MB/s | 395 MB/s | 0.75 | 130 KiB | 120 KiB |
| Deflate, table, 32 MiB (13028297 bytes compressed) | 270 MB/s | 237 MB/s | 0.88 | 130 KiB | 118 KiB |
| Deflate, random, 32 MiB (33564807 bytes compressed) | 7731 MB/s | 8405 MB/s | 1.09 | 82 KiB | 51 KiB |
| Deflate, zeros, 32 MiB (32750 bytes compressed) | 3773 MB/s | 4943 MB/s | 1.31 | 42 KiB | 7 KiB |
| Stored, 32 MiB | 9344 MB/s | 9594 MB/s | 1.03 | 80 KiB | 6 KiB |
| open an archive of 100000 entries (81 MiB) | 43.2 ms (432 ns per entry) | 9.1 ms (91 ns per entry) | 4.76 | 25.5 MiB (267 bytes per entry) | 26.7 MiB (280 bytes per entry) |
| open and read all 100000 entries | 751.7 ms (7517 ns per entry) | 791.2 ms (7912 ns per entry) | 0.95 | 36.3 MiB (380 bytes per entry) | 53.5 MiB (561 bytes per entry) |
<!-- bench:end -->

## Writing

What `zipwriter` produces against `archive/zip` from the same content: how long it takes and how large the archive
is. The compressor of gozipgo has one fixed algorithm (it has no levels), because its output is part of the
contract: the same content gives the same bytes with every version of Go. `compress/flate` makes no such promise.

<!-- bench-write:begin -->
Measured in the same run. Every writer gets the content in pieces of 32 KiB and writes to a destination that
only counts. `archive/zip` is measured twice: as it ships, and with `compress/flate` at its default level
registered as its compressor, which is the level requirement ZW-006 names. Sizes are bytes of the whole archive.
The largest size ratio on markup and tabular data is 1.078; ZW-006 allows 1.1.

| Case | gozipgo | archive/zip | compress/flate, default level | archive, gozipgo | archive, archive/zip | archive, compress/flate default | size, gozipgo / compress/flate default |
|---|---|---|---|---|---|---|---|
| Deflate, xml, 32 MiB | 72 MB/s | 48 MB/s | 22 MB/s | 7257414 | 7266999 | 6731617 | 1.078 |
| Deflate, table, 32 MiB | 55 MB/s | 47 MB/s | 32 MB/s | 13546825 | 13028297 | 12960172 | 1.045 |
| Deflate, random, 32 MiB | 200 MB/s | 114 MB/s | 113 MB/s | 33564802 | 33564807 | 33564807 | 1.000 |
| Deflate, zeros, 32 MiB | 1148 MB/s | 819 MB/s | 821 MB/s | 39058 | 32750 | 32750 | 1.193 |
| Stored, 32 MiB | 10709 MB/s | 10713 MB/s | 10662 MB/s | 33554562 | 33554562 | 33554562 | 1.000 |
| Deflate, 100000 entries of 1 to 4 KiB | 89 MB/s | 71 MB/s | 62 MB/s | 85674169 | 85916923 | 85845743 | 0.998 |
<!-- bench-write:end -->
