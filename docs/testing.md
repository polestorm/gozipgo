# Testy i porównania

Nacisk projektu: każda własność, którą obiecujemy, ma test uruchamialny z czystego klonu i liczbę, którą da się
odtworzyć. Archiwa testowe są budowane w teście, generowane skryptem z repozytorium albo pobierane według manifestu
z sumami.

## Skąd biorą się archiwa testowe

| Źródło | Do czego | Gdzie |
|---|---|---|
| **budowniczy** (`internal/testkit`): składa archiwum rekord po rekordzie z jawnymi wartościami każdego pola, także sprzecznymi | wymagania formatu, inwentarz (K1), archiwa wrogie (K6), wejścia startowe fuzzingu | kod w repozytorium; archiwa powstają w teście |
| **korpus zgodności**: małe archiwa z treścią wymyśloną, utworzone prawdziwymi programami | K2, K3 | `testdata/interop/` z manifestem (program, wersja, system, polecenie, SHA-256 archiwum i każdego wpisu); buduje go `tools/corpus` |
| **korpus wrogi**: archiwa z budowniczego zapisane jako pliki, żeby czarne skrzynki mogły je przeczytać | K3, `comparison.md` | generowane poleceniem z repozytorium do `out/`, z manifestem oczekiwanych kodów w repozytorium |
| **zestawy pobierane** (jeśli etap 0 je wskaże): publiczne zbiory archiwów testowych z przypiętym commitem | dodatkowe wejścia startowe i testy różnicowe | `testdata/sources.json`, `tools/fetch` |

## Warstwy

| Warstwa | Gdzie | Co sprawdza |
|---|---|---|
| inwentarz formatu | `requirements/format.md`, `TestFormatInventory` | każdy rekord, pole, bit flag, metoda kompresji i znany identyfikator pola dodatkowego z APPNOTE ma werdykt (obsługa albo kod) i archiwum, które go pokazuje (K1) |
| wymagania | `requirements/*.md`, `TestRequirementsCovered` | każde przyjęte wymaganie ma test; każde `REQ:` wskazuje istniejące wymaganie (K4) |
| wzorzec API | `api/<pakiet>.txt`, `TestAPISnapshot` | publiczne API równe wzorcowi; zmiana tylko przez `-update` i wpis w ADR (K4) |
| korpus zgodności | `TestInteropCorpus` | tryb ścisły: lista wpisów i SHA-256 treści równe manifestowi; wzorcowy zrzut listy co do bajta (K2) |
| wyrocznia | `TestFetchedArchives`, `FuzzReader` | archiwum przyjęte ściśle (zestawy pobrane, wejścia fuzzera): `archive/zip` daje tę samą listę nazw i tę samą treść; werdykty zestawów pobranych we wzorcu `testdata/verdicts/fetched.tsv` |
| dekompresja | `internal/inflate`: `TestAgainstOracle`, `TestBlockHeader`, `TestStoredBlock`, `TestLengthsAndDistances`, `TestDynamicCodes`, `TestTruncated`, `FuzzInflate` | strumienie budowane bit po bicie według RFC 1951 (każdy typ bloku, każdy błąd strumienia); zgodność z `compress/flate` na danych generowanych deterministycznie, na każdym poziomie kompresji |
| CRC-32 | `TestContentErrors` | CRC-32 liczy `hash/crc32` z biblioteki standardowej (wynik w całości wyznaczony przez definicję); test sprawdza, że niezgodność z katalogiem daje `zip-checksum` |
| wrogie archiwa | `TestHostileArchives` | każde archiwum z listy w `requirements/safety.md` odrzucone oczekiwanym kodem, w czasie i pamięci z K6 |
| złożoność | `TestPathological` | czas liniowy: wiele wpisów, długie nazwy, wiele pól dodatkowych, strumienie DEFLATE o najgorszym stosunku |
| bez wejścia i wyjścia | `TestNoIOImports` | pakiety biblioteki importują tylko pakiety z listy dozwolonych: nie `os`, `net`, `archive/zip`, `compress/flate` ani `fmt` |
| duże archiwa | `TestLargeArchive` | ZIP64: ponad 65 535 wpisów i wpis ponad 4 GB z syntetycznego `io.ReaderAt`, bez pliku na dysku; pamięć z K7 |
| powtarzalność | `TestDeterministic`, macierz CI | 10 przebiegów: te same listy, treści, kody błędów; wzorce zgodne na Linux, macOS i Windows; `go test -race` |
| czarne skrzynki | `tools/compare` (osobny moduł) | korpus zgodności i korpus wrogi przez Info-ZIP, 7-Zip, Python i `archive/zip`; tabela w `comparison.md` (K3) |
| odporność | fuzzery z `fuzzing.md`, `tools/fuzz` | K5 |
| wydajność | `Benchmark*`, `tools/bench` | K7; metoda w `methodology.md` |
| kompresja | `internal/deflate`: `TestRoundTrip`, `TestCodeLengths`, `TestSymbols`, `TestBlockForms`, `TestSplitIndependence`, `TestGolden`, `FuzzDeflate` | każdy strumień czytają własny dekoder i `compress/flate`; kody pełne, w limicie długości i optymalne; tabele symboli zgodne z RFC 1951; bajty niezależne od podziału zapisów i równe wzorcowi skrótów |
| pisarz | `zipwriter`: `TestRoundTrip`, `TestSmallestArchive`, `TestFixedFields`, `TestNames`, `TestZip64Forms`, `TestLarge`, `TestAddRaw`, `TestSinkErrors`, `TestGolden`, `FuzzRoundTrip`; `tools/compare` | zapis → odczyt ścisły i `archive/zip`; bajty najmniejszego archiwum pole po polu; ZIP64 dokładnie tam, gdzie trzeba; wpis ponad 4 GiB; błąd w każdym miejscu zapisu; te same bajty na każdym systemie i w dwóch wersjach Go (job `bytes` w CI); czarne skrzynki rozpakowują to samo |
| przykłady | `Example*` | przykłady z dokumentacji kompilują się i dają zapisany wynik |
| dowody | `tools/evidence` | pakiet K9 |

## Zasady

- test odwołuje się do wymagania komentarzem `REQ: PREFIKS-nnn`;
- archiwum niepoprawne powstaje w teście z budowniczego, z komentarzem, które pole jest zmienione; plików binarnych
  bez opisu pochodzenia w repozytorium nie ma;
- wzorce zmieniają się tylko przez `-update` i przegląd różnicy w commicie;
- błąd znaleziony przez fuzzing trafia do `testdata/fuzz/` pakietu jako wejście regresji i dostaje test nazwany;
- czarne skrzynki są wymienione z wersją; test, który ich potrzebuje, żyje w `tools/compare`, a nie w module
  głównym (moduł główny testuje się bez żadnych programów zewnętrznych, wyrocznią jest biblioteka standardowa);
- testy nie sięgają do sieci; test bez danych jest pomijany z komunikatem, którego pakiet dowodów nie liczy jako
  zaliczenia.

## Pomiary

| Data | Commit | K1: inwentarz (z werdyktem / wszystkie) | K2: korpus zgodności | K3: zgodność z czarnymi skrzynkami | K7: szybkość i pamięć | Uwagi |
|---|---|---|---|---|---|---|
| 2026-11-26 | `c976176` | 194 / 194 wierszy etapu 1 sprawdzonych archiwum; 21 wierszy (nagłówek lokalny, deskryptor, znacznik dzielenia) czeka na etap 2; razem 215 | 22 archiwa (Info-ZIP, 7-Zip, `ditto`, Python, Go, Eksplorator i 7-Zip z Windows 11): 12 czytanych ściśle zgodnie z manifestem, 10 wyjątków z `differences.md` (nazwy bez bitu 11: 9 w UTF-8 z macOS, 1 w CP852 z 7-Zipa na Windows), czytanych zgodnie z manifestem z opcją `UnflaggedNames` z manifestu. Zakres: lista wpisów (nazwy, rozmiary, CRC-32, metody); treść w etapie 2 | — | — | etapy 0 i 1. `go test ./...` (go1.26.6 darwin/arm64). Brak w korpusie: Java, Word, LibreOffice (uzupełnia właściciel, `tools/corpus -add`). Sprawdzenie fuzzerem: 4 krótkie przebiegi lokalne `FuzzReader`, razem 175 s, 2 wątki, ok. 5,4 mln wykonań, bez znalezisk (to nie kampania K5) |
| 2026-11-27 | `4a0326b` (v0.1.0) | 218 / 218 wierszy sprawdzonych archiwum (doszły 3 wiersze pól dodatkowych nagłówka lokalnego); nic nie czeka | 22 archiwa: 12 czytanych ściśle, 10 wyjątków z `differences.md` czytanych z opcją z manifestu; w obu grupach lista wpisów i SHA-256 treści każdego wpisu zgodne z manifestem | — | — | etap 2. `go test ./...` i `go test -race ./zipreader` (go1.26.6 darwin/arm64). Zestawy pobrane: 568 plików, 347 przyjętych ściśle, treść zgodna z `archive/zip` tam, gdzie obie strony czytają bez błędu. Sprawdzenie fuzzerem, lokalnie, 2 wątki: `FuzzInflate` 2 przebiegi, razem 85 s, ok. 5,4 mln wykonań; `FuzzReader` z niezmiennikami treści i wyrocznią 2 przebiegi, razem 145 s, ok. 8,0 mln wykonań; bez znalezisk (to nie kampania K5). Wstępny pomiar szybkości (`BenchmarkContent`, 8 MiB tekstu, Deflate): 187 MB/s wobec 173 MB/s `archive/zip`; to nie pomiar K7 |
| 2026-11-27 | `d84f0ab` | 218 / 218 | 22 archiwa: 12 czytanych ściśle, 10 z opcją z manifestu; lista wpisów i SHA-256 treści zgodne z manifestem | korpus zgodności: gozipgo 22/22, `archive/zip` 22/22, 7-Zip 22/22, `zipfile` Pythona 22/22, UnZip w wydaniu Apple 18/22, bsdtar 21/22 (różnice po stronie tych programów, `differences.md`); korpus wrogi: gozipgo odrzuca 39/39 oczekiwanym kodem, pozostałe czytają od 18 do 24 z 39. Narzędzie `tools/compare`, surowy wynik `results/compare.tsv`, metoda w `methodology.md`. Na Linuksie (krok informacyjny CI, UnZip w wydaniu Debiana) wszystkie czytniki mają 22/22. Do decyzji właściciela, czy wynik UnZip w wydaniu Apple (nie rozpakowuje nazw UTF-8) rozstrzyga kryterium | spełnione: czas odczytu treści względem `archive/zip` od 0,81 do 1,33 (próg 1,5), mediana z 7 przebiegów, 32 MiB na przypadek; pamięć odczytu stała (`TestMemory`); archiwum ZIP64 4,3 GB czytane przy 130 KiB alokacji (`TestLargeArchive`). `tools/bench`, `results/bench.tsv`, Apple M4, go1.26.6 | etap 3 (ADR 0004). K6: `TestHostileArchives` 39 archiwów w 14 rodzinach, najwolniejsze poniżej 1 ms, najwięcej 107 KiB alokacji; `TestLinearTime`, `TestMemory`. K7 (`tools/bench`, `results/bench.tsv`, Apple M4, go1.26.6, mediana z 7): czas odczytu treści względem `archive/zip` od 0,81 do 1,33 (próg 1,5); otwarcie archiwum o 100 000 wpisów 55 ms wobec 12 ms; `TestLargeArchive`: archiwum 4,3 GB, 8,6 GB treści, 130 KiB alokacji. K5: kampania 25 h zlecona na Synology na commicie `4a0326b`, w kolejce |
| 2026-11-27 | `c00125e` | 218 / 218 | 22 archiwa: 12 czytanych ściśle, 10 z opcją z manifestu; lista wpisów i SHA-256 treści zgodne z manifestem | korpus zgodności i korpus wrogi bez zmian: gozipgo 22/22 i 39/39 odrzuconych, `archive/zip`, 7-Zip, `zipfile` Pythona 22/22, UnZip w wydaniu Apple 18/22, bsdtar 21/22 (różnice po stronie tych programów, `differences.md`). Archiwa zapisane przez `zipwriter` (7): gozipgo w trybie ścisłym, `archive/zip`, 7-Zip, Python i bsdtar oddają treść wszystkich; UnZip w wydaniu Apple 6/7 (nazwy UTF-8). `tools/compare`, `results/compare.tsv`. Do decyzji właściciela, czy wynik UnZip w wydaniu Apple rozstrzyga kryterium | spełnione: czas odczytu treści względem `archive/zip` od 0,80 do 1,27 (próg 1,5), mediana z 7 przebiegów; `TestLargeArchive` i `TestMemory` bez zmian. `tools/bench`, `results/bench.tsv`, Apple M4, go1.26.6 | etap 4 (ADR 0005): pisarz i kompresor. ZW-006: archiwum 1,020 (znaczniki) i 1,052 (wiersze liczb) rozmiaru z `compress/flate` na poziomie domyślnym, próg 1,1. Zapis około 2 razy wolniejszy niż `archive/zip` (22 wobec 42 MB/s na znacznikach); kryterium szybkości zapisu nie ma. K8 dla pisarza: wzorce skrótów (`TestGolden`) zgodne na go1.26.0, go1.26.6 i go1.27.0 (darwin/arm64), w CI na trzech systemach i w zadaniu `bytes`. Sprawdzenie fuzzerem, lokalnie, 2 wątki: `FuzzRoundTrip` 2 przebiegi, razem 45 s; `FuzzDeflate` 25 s; bez znalezisk (to nie kampania K5) |

Zestawy pobrane (2026-11-26, po etapie 1): 568 plików z 9 zestawów, bez paniki, bez rozbieżności nazw
z `archive/zip`; werdykty w `testdata/verdicts/fetched.tsv`, omówienie w `differences.md`.

Kolumny K3, K7 i K8 czyta `tools/evidence`: wartość jest „niezmierzona”, dopóki w komórce stoi „—”; stan wymaga
słowa „spełnione” albo „niespełnione” w komórce. Kolumny K7 i K8 dochodzą przy pierwszym pomiarze (etap 3).
