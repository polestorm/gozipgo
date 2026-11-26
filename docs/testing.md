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
| pisarz (etap 4) | `TestRoundTrip`, `TestWriterAccepted`, `TestWriterStableAcrossGo` | zapis → odczyt ścisły; czarne skrzynki rozpakowują to samo; te same bajty w dwóch wersjach Go |
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

| Data | Commit | K1: inwentarz (z werdyktem / wszystkie) | K2: korpus zgodności | K3: zgodność z czarnymi skrzynkami | Uwagi |
|---|---|---|---|---|---|
| 2026-11-26 | `c976176` | 194 / 194 wierszy etapu 1 sprawdzonych archiwum; 21 wierszy (nagłówek lokalny, deskryptor, znacznik dzielenia) czeka na etap 2; razem 215 | 22 archiwa (Info-ZIP, 7-Zip, `ditto`, Python, Go, Eksplorator i 7-Zip z Windows 11): 12 czytanych ściśle zgodnie z manifestem, 10 wyjątków z `differences.md` (nazwy bez bitu 11: 9 w UTF-8 z macOS, 1 w CP852 z 7-Zipa na Windows), czytanych zgodnie z manifestem z opcją `UnflaggedNames` z manifestu. Zakres: lista wpisów (nazwy, rozmiary, CRC-32, metody); treść w etapie 2 | — | etapy 0 i 1. `go test ./...` (go1.26.6 darwin/arm64). Brak w korpusie: Java, Word, LibreOffice (uzupełnia właściciel, `tools/corpus -add`). Sprawdzenie fuzzerem: 4 krótkie przebiegi lokalne `FuzzReader`, razem 175 s, 2 wątki, ok. 5,4 mln wykonań, bez znalezisk (to nie kampania K5) |
| 2026-11-27 | — (drzewo robocze po `c976176`, przed commitem etapu 2) | 218 / 218 wierszy sprawdzonych archiwum (doszły 3 wiersze pól dodatkowych nagłówka lokalnego); nic nie czeka | 22 archiwa: 12 czytanych ściśle, 10 wyjątków z `differences.md` czytanych z opcją z manifestu; w obu grupach lista wpisów i SHA-256 treści każdego wpisu zgodne z manifestem | — | etap 2. `go test ./...` i `go test -race ./zipreader` (go1.26.6 darwin/arm64). Zestawy pobrane: 568 plików, 347 przyjętych ściśle, treść zgodna z `archive/zip` tam, gdzie obie strony czytają bez błędu. Sprawdzenie fuzzerem, lokalnie, 2 wątki: `FuzzInflate` 2 przebiegi, razem 85 s, ok. 5,4 mln wykonań; `FuzzReader` z niezmiennikami treści i wyrocznią 2 przebiegi, razem 145 s, ok. 8,0 mln wykonań; bez znalezisk (to nie kampania K5). Wstępny pomiar szybkości (`BenchmarkContent`, 8 MiB tekstu, Deflate): 187 MB/s wobec 173 MB/s `archive/zip`; to nie pomiar K7 |

Zestawy pobrane (2026-11-26, po etapie 1): 568 plików z 9 zestawów, bez paniki, bez rozbieżności nazw
z `archive/zip`; werdykty w `testdata/verdicts/fetched.tsv`, omówienie w `differences.md`.

Kolumny K3, K7 i K8 czyta `tools/evidence`: wartość jest „niezmierzona”, dopóki w komórce stoi „—”; stan wymaga
słowa „spełnione” albo „niespełnione” w komórce. Kolumny K7 i K8 dochodzą przy pierwszym pomiarze (etap 3).
