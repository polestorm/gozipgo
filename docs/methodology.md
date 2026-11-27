# Methodology of the comparisons

Every number and every row in this repository that compares gozipgo with something else leads here. This file says
what was measured, on what input, with which versions, on which machine and with which commands, so the measurement
can be repeated by anyone. Raw results are in [`results/`](results/).

Status: behaviour and speed were measured at stage 3 (2026-11-27). The writer is not measured yet.

## Rules

- **Input** is public or generated: archives of the conformance corpus (in the repository, each with the program,
  version and command that made it), archives built by the deterministic test builder in the repository, or sets
  fetched by `tools/fetch` according to `testdata/sources.json`. No private files.
- **The tool** that measures is in this repository (`go test`, `tools/compare`, `tools/bench`).
- **Candidates** are named with exact versions and how they were installed; their settings are stated.
- **Statistics**: the median of 5 runs unless stated otherwise; what counts as a failure is stated per table.
- **Raw output** of the run the numbers were taken from is committed under `results/`, with the date, the commit,
  the Go version, the operating system and the machine.
- When the input, a candidate's version, the method or the result changes, the measurement is run again with the
  same tool. Numbers are not edited by hand.

## Behaviour: `tools/compare`

```sh
go -C tools/compare run . -root ../..
```

writes `docs/results/compare.tsv` (one row per archive and reader) and the two tables of
[comparison.md](comparison.md). `TestCompareRecorded` checks the recorded file against the manifests.

**Input.** Two sets, both reproducible from the repository:

- the conformance corpus, `testdata/interop`: archives made by real programs, each with the program, version and
  command in `manifest.json`, which also records the SHA-256 of the content of every entry, computed when the
  corpus was made;
- the hostile corpus: 40 archives in the 14 families of requirement SAFE-003. They are built by the test builder
  of the repository, byte for byte the same on every system and Go version; `testdata/hostile.json` records what
  each one is, the code gozipgo gives and the SHA-256 of the archive. `go test ./zipreader -run
  TestHostileArchives -write-hostile DIR` writes them as files; the tool does that itself and checks the sums.

**Readers.** gozipgo at the commit named in the result, with zero `Options` (strict, default limits) and, for the
conformance archives the manifest lists as exceptions, the option the manifest names. `archive/zip` of the Go that
builds the tool, in process. The others as commands, in whatever version is installed, recorded in the result:

| Reader | Command |
|---|---|
| Info-ZIP unzip | `unzip -o -q ARCHIVE -d TARGET` |
| 7-Zip | `7zz x -y -bd -oTARGET ARCHIVE` |
| Python `zipfile` | `python3 -I -c SCRIPT ARCHIVE`: `ZipFile(...)`, then `read()` of every entry that is not a directory |
| bsdtar (libarchive) | `bsdtar -x -f ARCHIVE -C TARGET` |

A reader that is not installed is left out, and the result says which readers ran. Each command has 120 seconds.

**What a cell means.**

- *rejects*: `New`, `Open` or a read of gozipgo returned an error (the code is shown); `archive/zip` or Python
  raised an error while opening or reading any entry; a command ended with a failure status (unzip and 7-Zip: 2 or
  more; bsdtar: any non-zero status). "rejects after writing N" means the command failed but left N files behind.
- *reads* (*with a warning*: status 1 of unzip or 7-Zip): the reader produced files. For the commands these are
  the regular files found after extraction, anywhere under a scratch directory; the target directory is four levels
  deep inside it, so a file written outside the target is counted as *escapes* instead of being lost or doing harm.
- *same* (conformance corpus): the SHA-256 of the files the reader produced are exactly those of the non-directory
  entries in the manifest. For gozipgo the names must match too, wherever the archive stores them as ASCII or as
  UTF-8. Names are not compared for the commands, because the file system and the locale change them.
- the letter after "reads N" (hostile corpus) stands for the set of file contents; two readers with different
  letters in a row read different content from the same archive.

**Known limits.** The result describes the installed versions on one system. Apple's build of UnZip 6.00 cannot
create a file whose name is stored as UTF-8 with the UTF-8 flag (it fails on four conformance archives with exit
status 50), and bsdtar on macOS merges `__MACOSX` entries into the files they describe (one archive). Both are
recorded in [differences.md](differences.md). The continuous integration workflow runs the same tool on Linux as an
informational step; there Debian's build of UnZip agrees on all archives. That run is not a published result. Extraction programs have defences of their own, such as stripping
`../`; the table shows what was written and where, not whether a program is safe. The table is not a ranking.

## Speed and memory: `tools/bench`

```sh
go -C tools/bench run . -root ../..
```

writes `docs/results/bench.tsv` (every run) and the table of [comparison.md](comparison.md). `TestBenchRecorded`
checks criterion K7 on the recorded file.

**Input.** Generated by the tool with a fixed linear congruential generator, then written into an archive by
`archive/zip` (Deflate at its default level, or Store), so both readers read the same archive: 32 MiB of markup
with a small vocabulary ("xml"), of numeric rows ("table"), of bytes that do not compress ("random") and of zeros;
and an archive of 100,000 entries of 1 to 4 KiB of markup. The compressed bytes depend on the Go version, which the
result records.

**Readers.** gozipgo with `MaxEntries: -1` and `MaxRatio: -1` (the zeros case is above the default ratio limit),
everything else at its default: every local header is checked when the archive is opened and the size and CRC-32
of every entry while it is read. `archive/zip` as it is, and the `zip` package of `github.com/klauspost/compress`
(the version in `tools/bench/go.mod`) as it is; the two have the same API and run the same code of the tool. All
read from memory into the same 32 KiB buffer and close each entry reader. The K7 threshold is about `archive/zip`
only.

**Statistics.** One warm-up run, then 7 runs; the table shows the median. "Allocated" is the growth of
`runtime.MemStats.TotalAlloc` over the last run, that is, every byte allocated, not the peak. A reading case
counts for K7 when the median time of gozipgo is at most 1.5 times that of `archive/zip`.

**Opening is not reading.** `zipreader.New` reads every local header and data descriptor; `archive/zip.NewReader`
reads the central directory only. The row "open an archive of 100,000 entries" shows that cost; it is outside the
K7 threshold, which is about decompression.

**More than 4 GiB.** `TestLargeArchive` reads a ZIP64 archive of 4.3 GB from a synthetic `io.ReaderAt`: a Stored
entry of more than 4 GiB and a Deflate entry of more than 4 GiB of content placed after the fourth gigabyte,
8.6 GB of content in all, with size and CRC-32 checked, and asserts that less than 64 MiB is allocated (measured:
130 KiB). `TestMemory` asserts that the memory of `New` does not depend on the size of the data and that reading an
entry allocates the same for 1 MiB and for 32 MiB. `TestLinearTime` asserts that doubling the input at most
doubles the time, within noise.

**Known limits.** One machine, one Go version, data read from memory; no disk and no cold cache. Throughput on
real documents differs from the generated data.

## Writer

**Read back by other programs (`tools/compare`).** The tool builds seven archives with `zipwriter` from data it
generates with a fixed linear congruential generator (`tools/compare/written.go`): entries written by `Add`, by
`Create` and by `AddRaw`, both methods, a directory, empty files, names outside ASCII, `Header.Zip64`, 66,000
entries (ZIP64 end records) and an archive with no entries. Each archive goes to every reader of the behaviour
comparison, with the same commands; gozipgo reads with its default options except `MaxEntries: -1`. *same* means
the SHA-256 of the files the reader produced are exactly those of the content that was written, and for the
readers that list names (gozipgo, `archive/zip`, Python) the names too. The raw file also records the size and
the SHA-256 of every archive written, so two runs on different systems can be compared byte for byte.
`TestCompareRecorded` requires that gozipgo reads every one of them back. Sizes above 4 GiB are not in this
corpus: `TestLarge` covers them with the reader of this repository only.

**Speed and size (`tools/bench`).** The same content as for reading (32 MiB of each kind, and 100,000 entries of
1 to 4 KiB) is written three ways: by `zipwriter` through `Create`; by `archive/zip` as it ships; and by
`archive/zip` with `compress/flate` at `flate.DefaultCompression` registered as its compressor. The third exists
because the two differ: measured as a black box, `archive/zip` produces a larger archive than `compress/flate` at
its default level does. Every writer gets the content in pieces of 32 KiB and writes to a destination that only
counts bytes. One warm-up run, then 7 runs, median; the size is that of the whole archive. gozipgo has no
compression levels. It is measured twice: as it ships, where the writer decodes every DEFLATE stream it produces
and compares the size and the CRC-32 with the content (the self-check, requirement ZW-011), and with
`Options.SkipSelfCheck`; the tool stops if the two archives differ in size. The other writers have no such check,
so the column without it is the one that compares compressors and the default column is what a caller gets. `TestBenchWriteRecorded` checks on the recorded file that for markup and tabular data the
archive of gozipgo is at most 1.1 times the size of the one made with `compress/flate` at its default level
(requirement ZW-006). There is no threshold for the speed of writing.

**A third writer and the size of a program.** The writing table also has `klauspost/compress/zip` as it ships
(it has compression levels; the default is measured). The table "Size of a program" comes from the same run: the
tool writes four small programs into a scratch module, one that copies standard input to standard output and
three that read an archive from standard input and write its entries into a new archive with gozipgo,
`archive/zip` and `klauspost/compress`, builds each with `go build -trimpath -ldflags="-s -w"` and
`CGO_ENABLED=0`, and records the sizes of the binaries. "Added by the library" is the size minus that of the
copying program. The sources of the programs are in `tools/bench/main.go` (`footprintPrograms`).

**klauspost/compress in the behaviour comparison.** `tools/compare` runs it in process next to `archive/zip`,
with the same code, on all three corpora.

**The same bytes everywhere.** `internal/deflate/testdata/golden.tsv` and `zipwriter/testdata/golden.tsv` hold
the SHA-256 of what the compressor and the writer produce for fixed inputs. `TestGolden` in both packages
compares them on every system of the continuous integration workflow with the current Go release, and the job
`bytes` repeats it with the Go version named in `go.mod`. The files change only with `-update-golden` and an
entry in the change log.

## Known limits of the numbers

Stated in each section above.
