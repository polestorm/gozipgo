# Methodology of the comparisons

Every number and every row in this repository that compares gozipgo with something else leads here. This file says
what was measured, on what input, with which versions, on which machine and with which commands, so the measurement
can be repeated by anyone. Raw results are in [`results/`](results/).

Status: no measurements yet. Stage 3 fills in the sections below. A number or a comparison row is published only
when all of its section is filled in.

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

## Behaviour on hostile archives

To be filled in at stage 3: the hostile corpus (what each archive does and which rule of the specification it
breaks), the candidates (`archive/zip` of the stated Go version, Info-ZIP `unzip`, 7-Zip, Python `zipfile`), how
"accepts", "rejects" and "reads different content" are decided for each, commands, raw file.

## Agreement on the conformance corpus

To be filled in at stage 3: entry lists and SHA-256 of contents, per archive and per candidate.

## Speed and memory

To be filled in at stage 3: decompression throughput against `archive/zip` on the same data, opening an archive of
100,000 entries, memory per entry, the archive larger than 4 GB read from a synthetic source.

## Writer

To be filled in at stage 4: output size against `compress/flate`, byte identity across Go versions, acceptance by
the candidates.

## Known limits of the numbers

To be filled in with each measurement.
