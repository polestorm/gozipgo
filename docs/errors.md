# Error codes

Every error of the library about an archive is a `*zipreader.Error` with a fixed code. Messages (`Error.Message`)
are in English and may change between versions; the contract is the code, the entry (`Entry`, `Other`, `Name`) and
the offset, so a caller can translate or classify errors by code. A code, once released, keeps its meaning and is
not removed. `TestErrorCatalog` checks that this file lists exactly the codes the source defines and that a test
raises each of them.

Errors of the `io.ReaderAt` the caller supplied are not wrapped: `New` returns them unchanged. A source that ends
before the size the caller gave yields `io.ErrUnexpectedEOF`; a source that returns no bytes and no error yields
`io.ErrNoProgress`.

"APPNOTE" is APPNOTE.TXT 6.3.10, the .ZIP File Format Specification ([provenance.md](provenance.md)). Rules that
go beyond the specification are decisions of this project: ADR 0001 (one reading or none) and ADR 0002.

The catalogue grows with the library: this is the state after stage 1 (the central directory). Stage 2 adds the
codes of local headers, data descriptors, entry contents and DEFLATE.

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
8. conflicting names.

## Format and consistency (`zip-*`, `zip64-*`)

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `zip-no-directory-end` | `CodeNoDirectoryEnd` | APPNOTE 4.3.1, 4.3.16 | The last 22 + 65,535 bytes of the file hold no end of central directory record whose comment ends exactly at the end of the file. Files shorter than 22 bytes included. |
| `zip-multiple-directory-ends` | `CodeMultipleDirectoryEnds` | APPNOTE 4.3.1 ("MUST have only one") | Two records could each be the end of central directory: both end exactly at the end of the file (the second sits in the comment of the first). `Offset` is the earlier one. |
| `zip-trailing-data` | `CodeTrailingData` | APPNOTE 4.3.16, 4.4.25 | No record ends with the file, and an end of central directory record is followed by bytes after its comment. `Offset` is the first such byte. |
| `zip-directory-out-of-bounds` | `CodeDirectoryOutOfBounds` | APPNOTE 4.3.6, 4.4.23, 4.4.24 | Offset plus size of the central directory is not the position where the end records begin (the ZIP64 record when there is one, else the end of central directory record). Data before the archive, as in a self-extracting file, gives this code too. |
| `zip-directory-count-mismatch` | `CodeDirectoryCountMismatch` | APPNOTE 4.4.22, 4.4.23 | The directory cannot hold the declared number of headers (46 bytes each at least), ends before that many headers, or is larger than them. |
| `zip-directory-truncated` | `CodeDirectoryTruncated` | APPNOTE 4.3.12, 4.4.10–4.4.12 | A central directory header, with its name, extra field and comment, runs past the end of the directory. |
| `zip-directory-signature` | `CodeDirectorySignature` | APPNOTE 4.3.12 | Where a central directory header must start there is another signature. |
| `zip-extra-field` | `CodeExtraField` | APPNOTE 4.5.1 | The extra field is not a sequence of (header ID, data size, data) blocks that fills its declared length exactly. |
| `zip-directory-entry-size` | `CodeDirectoryEntrySize` | APPNOTE 4.3.8 | An entry whose name ends with `/` declares a non-zero compressed or uncompressed size. |
| `zip-comment-encoding` | `CodeCommentEncoding` | APPNOTE 4.4.4 bit 11, appendix D.2 | The comment of an entry with the UTF-8 flag is not valid UTF-8. |
| `zip64-inconsistent` | `CodeZip64Inconsistent` | APPNOTE 4.3.14, 4.3.15, 4.4.1.4, 4.5.3 | The ZIP64 structures are missing, misplaced or contradict the 32-bit ones: a field holds 0xFFFF or 0xFFFFFFFF and there is no ZIP64 locator or extra field; the locator does not point at a ZIP64 record that ends where the locator begins; a field of the ordinary record that is not 0xFFFF/0xFFFFFFFF differs from the ZIP64 record; the ZIP64 extra field does not hold exactly the fields the header refers to, or appears twice, or appears when no field refers to it. |

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

## Out of scope (`unsupported-*`)

Valid features the library does not implement. Four of the codes describe one entry rather than the archive:
the entry is listed, `Entry.Unsupported` holds the code, and opening the entry will return it (stage 2). When
several apply, the first in the order encryption, patch, method, version is given.

| Code | Constant | Rule | Meaning |
|---|---|---|---|
| `unsupported-encryption` | `CodeUnsupportedEncryption` | APPNOTE 4.4.4 bits 0, 6, 13; 4.5.12; 4.3.11; appendix E | Entry: bit 0 or bit 6 of the flags, a Strong Encryption Header (0x0017) or an AE-x structure (0x9901). Archive (`New` fails): bit 13 (masked local headers), or an archive extra data record where the directory starts. |
| `unsupported-patch` | `CodeUnsupportedPatch` | APPNOTE 4.4.4 bit 5, 4.5.8 | Entry: patch data (bit 5 or a Patch Descriptor, 0x000f). |
| `unsupported-method` | `CodeUnsupportedMethod` | APPNOTE 4.4.5 | Entry: a compression method other than 0 (Stored) and 8 (Deflate). |
| `unsupported-version` | `CodeUnsupportedVersion` | APPNOTE 4.4.3 | Entry: version needed to extract above 4.5. Archive: the same in the ZIP64 end of central directory record (a version 2 record, used with an encrypted or compressed directory, needs 6.2). |
| `unsupported-multi-volume` | `CodeUnsupportedMultiVolume` | APPNOTE 4.4.13, 4.4.19–4.4.21, 4.3.15, section 8 | A disk number other than 0, a number of entries on this disk other than the total, or a ZIP64 locator whose total number of disks is not 1. |
| `unsupported-flag` | `CodeUnsupportedFlag` | APPNOTE 4.4.4 | A reserved or unused bit of the general purpose flag is set: 4, 7, 8, 9, 10, 12, 14 or 15. |
| `unsupported-digital-signature` | `CodeUnsupportedDigitalSignature` | APPNOTE 4.3.13 | A digital signature record in the central directory. |
| `unsupported-zip64-data` | `CodeUnsupportedZip64Data` | APPNOTE 4.3.14.3, 4.4.27 | The ZIP64 end of central directory record has an extensible data sector. |
