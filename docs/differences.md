# Różnice i wyłączenia

Każdy wpis powstaje razem z testem, który różnicę pokazuje, i wskazuje regułę specyfikacji albo decyzję z `adr/`.

## Archiwa, które inne programy otwierają, a `gozipgo` w trybie ścisłym odrzuca

| Archiwum albo rodzina | Kod | Kto otwiera | Powód odrzucenia | Opcja poluzowania |
|---|---|---|---|---|
| nazwa spoza ASCII zapisana w UTF-8 bez bitu 11 (`zip` i `ditto` z macOS) | `name-encoding` | `archive/zip` (wydaje nazwę z oznaczeniem `NonUTF8`); pozostałe czarne skrzynki: pomiar w etapie 3 | APPNOTE, dodatek D.2: bez bitu 11 nazwa jest w CP437, w praktyce w kodowaniu systemu; archiwum nie mówi, w którym, więc nazwa ma więcej niż jedno odczytanie (ADR 0002 pkt 6). Test: `TestInteropCorpus`, `TestUnflaggedNames` | `Options.UnflaggedNames: UTF8` albo `CP437`; użycie widoczne w `Relaxed()` |
| dane przed archiwum (plik samorozpakowujący) | `zip-directory-out-of-bounds` | pomiar w etapie 3 | katalog nie kończy się tam, gdzie mówi koniec katalogu; to samo miejsce służy do przemycania treści (ADR 0001 pkt 5). Test: `TestDirectoryBounds` | planowana `AllowPrefix` (etap 2, pytanie 3) |
| pole Unicode Path (0x7075) o CRC-32 innym niż suma pola nazwy | `name-unicode-path` | programy stosujące APPNOTE 4.6.9 dosłownie (pole „SHOULD be ignored”) | program, który CRC nie sprawdza, wziąłby drugą nazwę (ADR 0002 pkt 8). Test: `TestUnicodePath`, wiersz `extra.0x7075.mismatch` inwentarza | brak |
| sygnatura lokalizatora ZIP64 przed końcem katalogu bez spójnych rekordów ZIP64 | `zip64-inconsistent` | programy, które lokalizatora szukają tylko przy wartościach granicznych | program, który woli rekordy ZIP64, przeczytałby inny katalog (ADR 0002 pkt 4). Test: `TestZip64` | brak |

## Miejsca, w których przyjmujemy więcej, niż mówi litera specyfikacji

| Cecha | Reguła APPNOTE | Co robimy | Powód |
|---|---|---|---|
| plik o rozmiarze zero z niepustymi danymi skompresowanymi | 4.3.8: „Zero-byte files … MUST NOT include file data” | przyjmujemy (wiersz `cdh.empty-file-with-data` inwentarza); wpis katalogu z danymi odrzucamy | Python `zipfile` (2 bajty) i Go `archive/zip` (5 bajtów) zapisują pusty plik metodą Deflate jako pusty strumień: `python-deflated.zip`, `go-deflate.zip` w korpusie. Treść sprawdzi `Open` (etap 2) |

| pole dodatkowe ZIP64 w postaci pełnej (także pola, do których nagłówek nie odsyła) | 4.5.3: pola „MUST only appear if the corresponding … field is set to 0xFFFF or 0xFFFFFFFF” | przyjmujemy, gdy każde odczytanie daje te same wartości (wiersz `extra.0x0001.full-form`, `TestZip64ExtraFullForm`); inaczej `zip64-inconsistent` | archiwum `test/success/zip64.zip` z zestawu yauzl (`testdata/sources.json`) |

## Co pokazały zestawy pobrane (2026-11-26)

568 plików z dziewięciu zestawów (`testdata/sources.json`): bez paniki, każdy błąd ze stałym kodem, a żadne
archiwum przyjęte ściśle nie ma w `archive/zip` innej listy nazw. Werdykt każdego pliku: `testdata/verdicts/fetched.tsv`
(`TestFetchedArchives`). Zestawy służą rozwojowi; to nie jest porównanie z konkurencją (K3 mierzy etap 3 według
`methodology.md`). Najważniejsze przypadki, w których `archive/zip` (Go 1.26.6) przyjmuje, a `gozipgo` odrzuca:

| Plik | Kod | Co to jest |
|---|---|---|
| `poi-opc/test-data/document/unicode-path.docx` | `name-unicode-path` | dokument Worda, w którym pola Unicode Path zamieniają nazwy `word/document.xml` i pliku dodatkowego: program czytający Unicode Path widzi inną treść dokumentu niż program, który go nie czyta. Dokładnie przypadek z ADR 0002 pkt 8 |
| `corkami-zip/zip/unicode.zip` | `name-unicode-path` | Unicode Path użyty zgodnie z przeznaczeniem: nazwa w nagłówku to `dg`, w polu emoji. Odrzucamy z tego samego powodu: dwa odczytania nazwy |
| `libzip-regress/…/zip-in-archive-comment.zip` | `zip-multiple-directory-ends` | całe drugie archiwum w komentarzu pierwszego |
| 55 plików `.doc` z `poi-opc` | `zip-trailing-data` | dokumenty binarne OLE z osadzonym pakietem ZIP: `archive/zip` znajduje w nich archiwum, my wymagamy, żeby plik kończył się rekordem końca katalogu |
| `libzip-regress/…/extra_field_align_*.zip` (12 plików) | `zip-extra-field` | 1–3 bajty dopełnienia po ostatnim bloku pól dodatkowych; APPNOTE 4.5.1 takiej postaci nie zna |
| `cpython-zip/exe_with_zip`, `go-archive-zip/test-prefix.zip`, `libzip-regress/…/junk-at-start.zip` | `zip-directory-out-of-bounds`, `zip-trailing-data` | dane przed archiwum (pliki samorozpakowujące); `exe_with_z64` daje `zip64-inconsistent`, bo lokalizator ZIP64 wskazuje położenie liczone bez danych wstępnych. Do rozstrzygnięcia z opcją `AllowPrefix` w etapie 2 |
| `libzip-regress/…/incons-*.zip` | różne | zestaw niespójności libzip: te, które dotyczą katalogu, odrzucamy; te, które dotyczą nagłówków lokalnych i treści, czekają na etap 2 |
| `fifield-zipbomb/zbsm.zip` | (przyjęte) | bomba o nakładających się wpisach: katalog jest spójny, nakładanie wykryje etap 2 (ENT-004); to archiwum jest sprawdzianem etapu 2 |

## Korpus zgodności: wyjątki (K2)

Stan z 2026-11-26 (etap 1: lista wpisów). Każde z tych archiwów jest odrzucane w trybie ścisłym kodem
`name-encoding` i czytane zgodnie z manifestem z opcją `UnflaggedNames` podaną w polu `expect` manifestu: `UTF8`
dla archiwów z macOS, `CodePage` z tablicą CP852 dla archiwum 7-Zipa z polskiego Windows.

| Archiwum | Program i wersja | Powód |
|---|---|---|
| `infozip-default.zip`, `infozip-stored.zip`, `infozip-best.zip`, `infozip-comment.zip`, `infozip-stream.zip`, `infozip-zip64.zip`, `infozip-noextra.zip` | Info-ZIP Zip 3.0 (July 5th 2008) z modyfikacjami Apple, macOS 27.0.1 | nazwa „zażółć gęślą jaźń.txt” zapisana w UTF-8 bez bitu 11 i bez pola Unicode Path |
| `macos-ditto.zip`, `macos-ditto-rsrc.zip` | `ditto` z macOS 27.0.1 | to samo |
| `windows11-7zip.zip` | 7-Zip na polskim Windows 11 (wersja niezapisana) | nazwy z polskimi literami zapisane w stronie OEM systemu (CP852) bez bitu 11; nazwa w UTF-8 jest tylko w polu Unicode Path nagłówka lokalnego, katalog centralny jej nie ma. Jako CP437 archiwum też się czyta, ale daje inne znaki („Agresêaw”). `TestSameFolderTwoWriters` porównuje wynik z archiwum Eksploratora tego samego folderu |

## Dekompresja względem `compress/flate`

Strumienie, dla których werdykt (poprawny albo błędny) różni się, z odwołaniem do RFC 1951.

| Strumień | `gozipgo` | `compress/flate` | Reguła |
|---|---|---|---|
| — | — | — | — |
