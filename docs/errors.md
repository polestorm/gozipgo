# Error codes

Every error of the reader about an archive is a `*zipreader.Error` with a fixed code, and every error of the
writer about what it was asked to write is a `*zipwriter.Error` with a fixed code ([the writer](#the-writer-write--and-name-)).
Messages (`Error.Message`) are in English and may change between versions; the contract is the code, the entry
(`Entry`, `Other`, `Name`) and, for the reader, the offset, so a caller can translate or classify errors by code.
A code, once released, keeps its meaning and is not removed. `TestErrorCatalog` checks that this file lists
exactly the codes the source defines and that a test raises each of them.

Errors of the `io.ReaderAt` the caller supplied are not wrapped: `New` and the readers of `Open` and `OpenRaw`
return them unchanged. A source that ends before the size the caller gave yields `io.ErrUnexpectedEOF`; a source
that returns no bytes and no error yields `io.ErrNoProgress`. Two more errors are not about an archive and have no
code: `zipreader.ErrNoEntry` (an `Entry` that does not belong to the reader) and `zipreader.ErrClosed` (a read
after `Close`).

"APPNOTE" is APPNOTE.TXT 6.3.10, the .ZIP File Format Specification, and "RFC 1951" is the DEFLATE specification
([provenance.md](provenance.md)). Rules that go beyond the specifications are decisions of this project: ADR 0001
(one reading or none), ADR 0002 (the directory) and ADR 0003 (local headers, layout and content).

Errors come from two places. `New` reports everything that can be known without decompressing: the directory,
the local headers, the data descriptors and the layout of the file. `Open` and the reader it returns report what
is about the content of one entry: `unsupported-*` for the entry, `limit-entry-size`, `limit-ratio`,
`limit-total-size`, `zip-size-mismatch` (content), `zip-checksum` and `inflate-*`.

## When an archive has more than one fault

`New` reports the fault found first in this fixed order, which is part of the contract and has a test
(`TestErrorOrder`):

1. the end of central directory record;
2. the ZIP64 locator and record;
3. multi-volume fields;
4. the bounds of the central directory;
5. the declared number of entries against the declared size of the directory;
6. limits on the archive comment, the number of entries and the size of the directory;
7. the directory headers in order; within one header: signature, truncation, flags, limits on lengths, name,
   structure of the extra field, ZIP64 extra field, disk number, Unicode Path, comment, size of a directory entry;
8. conflicting names;
9. local header offsets, in the order of the directory: each must leave room for a header before the directory;
10. the entries in the order of their position in the file; within one entry: signature of the local header,
    flags, method, name, room for the header, structure of its extra field, its ZIP64 extra field, CRC-32 and
    sizes, Unicode Path, encryption or patch extra fields, overlap with the next entry or the directory, data
    descriptor or unreferenced bytes after the data, sizes of a Stored entry.

Bytes before the first entry are reported between steps 9 and 10.

Reading the content of an entry reports the first fault of the stream; at its end, in this order: bytes of
compressed data after the DEFLATE stream, content shorter than declared, CRC-32. Content longer than declared is
reported as soon as the declared size has been delivered. The bytes delivered before an error do not depend on
the size of the buffers the caller reads with.

## Format and consistency (`zip-*`, `zip64-*`)

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `zip-no-directory-end` | `CodeNoDirectoryEnd` | APPNOTE 4.3.1, 4.3.16 | The last 22 + 65,535 bytes of the file hold no end of central directory record whose comment ends exactly at the end of the file. Files shorter than 22 bytes included. |
| `zip-multiple-directory-ends` | `CodeMultipleDirectoryEnds` | APPNOTE 4.3.1 ("MUST have only one") | Two places could each be the end of central directory. Either two records both end exactly at the end of the file (the second sits in the comment of the first; `Offset` is the earlier one), or the signature of an end record occurs inside the record that ends with the file or in its comment (`Offset` is that signature): programs that take the last signature without checking the comment length would read another directory. |
| `zip-trailing-data` | `CodeTrailingData` | APPNOTE 4.3.16, 4.4.25 | No record ends with the file, and an end of central directory record is followed by bytes after its comment. `Offset` is the first such byte. |
| `zip-directory-out-of-bounds` | `CodeDirectoryOutOfBounds` | APPNOTE 4.3.6, 4.4.23, 4.4.24 | Offset plus size of the central directory is not the position where the end records begin (the ZIP64 record when there is one, else the end of central directory record). Data before the archive, as in a self-extracting file, gives this code too. |
| `zip-directory-count-mismatch` | `CodeDirectoryCountMismatch` | APPNOTE 4.4.22, 4.4.23 | The directory cannot hold the declared number of headers (46 bytes each at least), ends before that many headers, or is larger than them. |
| `zip-directory-truncated` | `CodeDirectoryTruncated` | APPNOTE 4.3.12, 4.4.10–4.4.12 | A central directory header, with its name, extra field and comment, runs past the end of the directory. |
| `zip-directory-signature` | `CodeDirectorySignature` | APPNOTE 4.3.12 | Where a central directory header must start there is another signature. |
| `zip-extra-field` | `CodeExtraField` | APPNOTE 4.5.1 | The extra field is not a sequence of (header ID, data size, data) blocks that fills its declared length exactly. |
| `zip-directory-entry-size` | `CodeDirectoryEntrySize` | APPNOTE 4.3.8 | An entry whose name ends with `/` declares a non-zero compressed or uncompressed size. |
| `zip-comment-encoding` | `CodeCommentEncoding` | APPNOTE 4.4.4 bit 11, appendix D.2 | The comment of an entry with the UTF-8 flag is not valid UTF-8. |
| `zip64-inconsistent` | `CodeZip64Inconsistent` | APPNOTE 4.3.14, 4.3.15, 4.4.1.4, 4.5.3 | The ZIP64 structures are missing, misplaced or contradict the 32-bit ones: a field holds 0xFFFF or 0xFFFFFFFF and there is no ZIP64 locator or extra field; the locator does not point at a ZIP64 record that ends where the locator begins; a field of the ordinary record that is not 0xFFFF/0xFFFFFFFF differs from the ZIP64 record; the ZIP64 extra field does not hold exactly the fields the header refers to, or appears twice, or appears when no field refers to it. In a local header: a size is 0xFFFFFFFF and there is no ZIP64 extra field, the field does not hold exactly the two sizes, appears twice, or differs from a size field that is not 0xFFFFFFFF. With `Options.AllowUnusedZip64` a ZIP64 field of zeros in a local header next to sizes without the marker is not an error. |
| `zip-local-header-mismatch` | `CodeLocalHeaderMismatch` | APPNOTE 4.3.7, 4.4.4, 4.4.16; ADR 0003 | The offset in the directory does not leave room for a local header before the directory, there is no local header signature there, or the local header contradicts the directory: flags, compression method, name; CRC-32 or a size (with bit 3 of the flags they may be zero instead); a header that runs into the directory; an encryption or patch extra field the directory does not have. The version needed and the date and time are not compared. |
| `zip-descriptor-mismatch` | `CodeDescriptorMismatch` | APPNOTE 4.3.9, 4.4.4 bit 3; ADR 0003 | An entry with bit 3 of the flags is not followed, up to the next entry or the directory, by exactly one data descriptor of 12, 16, 20 or 24 bytes (without or with the signature, 4-byte or 8-byte sizes), the 16-byte or 24-byte form has no signature, or the descriptor differs from the directory. `Offset` is the end of the data. |
| `zip-entries-overlap` | `CodeEntriesOverlap` | ADR 0001, ADR 0003 | The local header and data of an entry reach into the next entry or into the central directory; two directory entries with the same local header offset included. `Entry` is the later entry of the pair in the directory and `Other` the earlier one, or -1 when the entry reaches into the directory. |
| `zip-unreferenced-data` | `CodeUnreferencedData` | APPNOTE 4.3.6; ADR 0001, ADR 0003 | Bytes before the central directory that belong to no entry: before the first entry (`Offset` 0; `Options.AllowPrefix` accepts them), after the data of an entry without bit 3, or before the directory of an archive with no entries. A spanning marker before the first entry gives this code too. |
| `zip-size-mismatch` | `CodeSizeMismatch` | APPNOTE 4.4.8, 4.4.9 | From `New`: a Stored entry whose compressed and uncompressed sizes differ. From reading: the content is longer or shorter than the uncompressed size the directory declares. |
| `zip-checksum` | `CodeChecksum` | APPNOTE 4.4.7 | The CRC-32 of the content differs from the one the directory declares. Returned by the read that would otherwise return `io.EOF`. |

## DEFLATE (`inflate-*`)

Returned while the content of a Deflate entry is read. `Offset` is the position in the file where the fault was
found (for `inflate-trailing-data`, the start of the data).

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `inflate-block-type` | `CodeInflateBlockType` | RFC 1951, 3.2.3 | A block of the reserved type (BTYPE 11). |
| `inflate-stored-length` | `CodeInflateStoredLength` | RFC 1951, 3.2.4 | In a stored block NLEN is not the one's complement of LEN. |
| `inflate-code-lengths` | `CodeInflateCodeLengths` | RFC 1951, 3.2.2, 3.2.7; ADR 0003 | The description of the dynamic codes does not give Huffman codes: more than 286 literal/length codes or more than 30 distance codes; a code length code or literal/length code that is not complete (over-subscribed or with unused codes); a distance code that is neither complete, nor empty, nor a single code of one bit; a repeat with no previous length or past the end of the lengths; no code for the end of the block. |
| `inflate-distance` | `CodeInflateDistance` | RFC 1951, 3.2.3, 3.2.5, 3.2.6 | A distance that reaches before the start of the output, a length symbol 286 or 287, a distance symbol 30 or 31, a length in a block with no distance codes, or bits that are no code of an incomplete distance code. |
| `inflate-truncated` | `CodeInflateTruncated` | RFC 1951, 3.2.3; APPNOTE 4.4.8 | The compressed data of the entry end before the last block of the stream does. |
| `inflate-trailing-data` | `CodeInflateTrailingData` | APPNOTE 4.4.8; ADR 0003 | The stream ends before the compressed data of the entry do: the entry holds bytes that are not part of its content. |

## Entry names (`name-*`)

`Error.Name` is the rejected name as stored. Checks run in the order of this table.

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `name-empty` | `CodeNameEmpty` | APPNOTE 4.4.10; ADR 0001 | The name has length zero. |
| `name-nul` | `CodeNameNUL` | ADR 0001 | The name has a zero byte. |
| `name-encoding` | `CodeNameEncoding` | APPNOTE 4.4.4 bit 11, appendix D.2; ADR 0002 | With the UTF-8 flag: the name is not valid UTF-8. Without it: the name has bytes outside ASCII and `Options.UnflaggedNames` is `ASCIIOnly` (the default), or it is `UTF8` and the name is not valid UTF-8, or it is `CodePage` and `Options.CodePage` is nil or has no character at or above U+0080 for a byte of the name. |
| `name-control-character` | `CodeNameControl` | ADR 0002 | The name has a character below U+0020 or U+007F. |
| `name-backslash` | `CodeNameBackslash` | APPNOTE 4.4.17.1 | The name has a backslash. |
| `name-absolute` | `CodeNameAbsolute` | APPNOTE 4.4.17.1 | The name starts with `/` or with a drive letter and a colon. |
| `name-traversal` | `CodeNameTraversal` | ADR 0001 | A path segment is `..`, `.` or empty (`a//b`). |
| `name-unicode-path` | `CodeNameUnicodePath` | APPNOTE 4.6.9; ADR 0002 | An Info-ZIP Unicode Path extra field (0x7075) that is not version 1, has a CRC-32 that is not the checksum of the name field, appears twice, or gives a name other than the name of the entry. |
| `name-duplicate` | `CodeNameDuplicate` | ADR 0001, ADR 0002 | Two entries cannot both exist in one tree: the same name, a file `a` and a directory `a/`, or a file `a` and any entry `a/…`. With `Options.FoldCase` ASCII letters are compared without case. `Entry` is the later entry of a conflicting pair and `Other` the earlier one. |

## Limits (`limit-*`)

Each limit is a field of `zipreader.Options`; zero means the default and a negative value means no limit.

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `limit-entries` | `CodeLimitEntries` | `Options.MaxEntries`, default 65,536 | The end of central directory record declares more entries. Checked before the directory is read. |
| `limit-directory-size` | `CodeLimitDirectorySize` | `Options.MaxDirectorySize`, default 64 MiB | The central directory is larger. Checked before the directory is read. |
| `limit-name-length` | `CodeLimitNameLength` | `Options.MaxNameLength`, default 4,096 bytes | The name of an entry, as stored, is longer. |
| `limit-comment-length` | `CodeLimitCommentLength` | `Options.MaxCommentLength`, default 65,535 bytes | The archive comment or the comment of an entry is longer. |
| `limit-extra-length` | `CodeLimitExtraLength` | `Options.MaxExtraLength`, default 65,535 bytes | The extra field of an entry is longer. |
| `limit-entry-size` | `CodeLimitEntrySize` | `Options.MaxEntrySize`, default 1 GiB | From `Open`: the directory declares more content for the entry. Reading never delivers more than the declared size, so the limit holds for the real content. |
| `limit-ratio` | `CodeLimitRatio` | `Options.MaxRatio`, default 1,000 | From `Open`: the entry declares more than 1 MiB of content and more than `MaxRatio` bytes of content for each byte of compressed data. |
| `limit-total-size` | `CodeLimitTotalSize` | `Options.MaxTotalSize`, default 4 GiB | From reading: the content delivered by all readers of the `Reader` together reached the limit. The bytes up to the limit are delivered; an entry opened twice counts twice. |

## Out of scope (`unsupported-*`)

Valid features the library does not implement. Four of the codes describe one entry rather than the archive:
the entry is listed, `Entry.Unsupported` holds the code, `Open` returns it and `OpenRaw` still gives the stored
bytes. When several apply, the first in the order encryption, patch, method, version is given.

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `unsupported-encryption` | `CodeUnsupportedEncryption` | APPNOTE 4.4.4 bits 0, 6, 13; 4.5.12; 4.3.11; appendix E | Entry: bit 0 or bit 6 of the flags, a Strong Encryption Header (0x0017) or an AE-x structure (0x9901). Archive (`New` fails): bit 13 (masked local headers), or an archive extra data record where the directory starts. |
| `unsupported-patch` | `CodeUnsupportedPatch` | APPNOTE 4.4.4 bit 5, 4.5.8 | Entry: patch data (bit 5 or a Patch Descriptor, 0x000f). |
| `unsupported-method` | `CodeUnsupportedMethod` | APPNOTE 4.4.5 | Entry: a compression method other than 0 (Stored) and 8 (Deflate). |
| `unsupported-version` | `CodeUnsupportedVersion` | APPNOTE 4.4.3 | Entry: version needed to extract above 4.5. Archive: the same in the ZIP64 end of central directory record (a version 2 record, used with an encrypted or compressed directory, needs 6.2). With `Options.AllowVersionHostByte` only the lower byte of the field counts for an entry. |
| `unsupported-multi-volume` | `CodeUnsupportedMultiVolume` | APPNOTE 4.4.13, 4.4.19–4.4.21, 4.3.15, section 8 | A disk number other than 0, a number of entries on this disk other than the total, or a ZIP64 locator whose total number of disks is not 1. |
| `unsupported-flag` | `CodeUnsupportedFlag` | APPNOTE 4.4.4 | A reserved or unused bit of the general purpose flag is set: 4, 7, 8, 9, 10, 12, 14 or 15. |
| `unsupported-digital-signature` | `CodeUnsupportedDigitalSignature` | APPNOTE 4.3.13 | A digital signature record in the central directory. |
| `unsupported-zip64-data` | `CodeUnsupportedZip64Data` | APPNOTE 4.3.14.3, 4.4.27 | The ZIP64 end of central directory record has an extensible data sector. |

## The writer (`write-*` and `name-*`)

`zipwriter` refuses a name with the code the reader gives for it: `name-empty`, `name-nul`, `name-encoding` (the
name is not valid UTF-8), `name-control-character`, `name-backslash`, `name-absolute`, `name-traversal` and
`name-duplicate`, under constants of the same names (`zipwriter.CodeNameEmpty` and so on). The checks run in a
fixed order: the state of the writer (`write-after-close`, `write-entry-open`), the name in the order of the list
above, `write-name-length`, `write-method`, `write-timestamp`, `name-duplicate`, and then what is specific to the
call (`write-directory-content`, `write-raw-content` for Stored sizes that differ).

An error found before the first byte of an entry is written leaves the writer as it was: the call can be repeated
with a corrected header. An error found after that (`write-entry-size`, `write-raw-content` found while
copying, and `write-self-check` of an entry started by `Create`) means the archive cannot be completed: every
later call returns the same error. The same holds for an
error of the `io.Writer` the archive is written to and of the `io.Reader` given to `AddRaw`, which are returned
unchanged; a write that is short without an error yields `io.ErrShortWrite`, a reader that returns no bytes and no
error yields `io.ErrNoProgress`.

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `write-name-length` | `CodeNameLength` | APPNOTE 4.4.10 | The name is longer than 65,535 bytes, the limit of the name length field. |
| `write-method` | `CodeMethod` | APPNOTE 4.4.5; ADR 0001 | `Header.Method` is neither `Stored` (0) nor `Deflate` (8). |
| `write-timestamp` | `CodeTimestamp` | APPNOTE 4.4.6 | `Header.Modified` is not a date the MS-DOS format records: a year outside 1980 to 2107, a day the month does not have, an odd or out-of-range second. The zero value is 1980-01-01 00:00:00. |
| `write-directory-content` | `CodeDirectoryContent` | ADR 0002 (DIR-011) | Content for an entry whose name ends with "/": the reader rejects a directory entry with a size. |
| `write-entry-open` | `CodeEntryOpen` | ADR 0005 | `Add`, `AddRaw`, `Create` or `Close` while the entry started by `Create` has not been closed. |
| `write-after-close` | `CodeAfterClose` | ADR 0005 | A new entry after `Close` of the writer (`Entry` is -1), or `Write` to an entry after its `Close`. |
| `write-entry-size` | `CodeEntrySize` | APPNOTE 4.3.9.2, 4.5.3 | An entry started by `Create` without `Header.Zip64` reached 4 GiB − 1 bytes, compressed or not: its local header, already written, does not announce 64-bit sizes. The archive cannot be completed. |
| `write-raw-content` | `CodeRawContent` | ADR 0005 | The data given to `AddRaw` are not what `RawInfo` says: Stored data with two different sizes (found before anything is written), data that end early, a stream that is not DEFLATE or does not end with the data, content of another size or CRC-32. |
| `write-self-check` | `CodeSelfCheck` | ADR 0007 | The writer decodes every DEFLATE stream it writes and compares the size and the CRC-32 of the result with the content it was given; they differ, or the stream does not end with its last byte. It means a defect of the compressor of this module, not of the caller: please report it. `Add` finds it before anything is written and the archive can be completed without the entry; an entry started by `Create` is already in the archive, which cannot be completed. |
