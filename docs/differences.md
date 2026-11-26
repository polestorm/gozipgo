# Różnice i wyłączenia

Każdy wpis powstaje razem z testem, który różnicę pokazuje, i wskazuje regułę specyfikacji albo decyzję z `adr/`.

## Archiwa, które inne programy otwierają, a `gozipgo` w trybie ścisłym odrzuca

| Archiwum albo rodzina | Kod | Kto otwiera | Powód odrzucenia | Opcja poluzowania |
|---|---|---|---|---|
| nazwa spoza ASCII zapisana w UTF-8 bez bitu 11 (`zip` i `ditto` z macOS) | `name-encoding` | `archive/zip` (wydaje nazwę z oznaczeniem `NonUTF8`); pozostałe czarne skrzynki: pomiar w etapie 3 | APPNOTE, dodatek D.2: bez bitu 11 nazwa jest w CP437, w praktyce w kodowaniu systemu; archiwum nie mówi, w którym, więc nazwa ma więcej niż jedno odczytanie (ADR 0002 pkt 6). Test: `TestInteropCorpus`, `TestUnflaggedNames` | `Options.UnflaggedNames: UTF8` albo `CP437`; użycie widoczne w `Relaxed()` |
| dane przed archiwum (plik samorozpakowujący) | `zip-directory-out-of-bounds` | pomiar w etapie 3 | katalog nie kończy się tam, gdzie mówi koniec katalogu; to samo miejsce służy do przemycania treści (ADR 0001 pkt 5). Test: `TestDirectoryBounds` | planowana `AllowPrefix` (etap 2, pytanie 3) |
| pole Unicode Path (0x7075) o CRC-32 innym niż suma pola nazwy | `name-unicode-path` | programy stosujące APPNOTE 4.6.9 dosłownie (pole „SHOULD be ignored”) | program, który CRC nie sprawdza, wziąłby drugą nazwę (ADR 0002 pkt 8). Test: `TestUnicodePath`, wiersz `extra.0x7075.mismatch` inwentarza | brak |
| sygnatura lokalizatora ZIP64 przed końcem katalogu bez spójnych rekordów ZIP64 | `zip64-inconsistent` | programy, które lokalizatora szukają tylko przy wartościach granicznych | program, który woli rekordy ZIP64, przeczytałby inny katalog (ADR 0002 pkt 4). Test: `TestZip64` | brak |
| nagłówek lokalny sprzeczny z katalogiem: inne flagi, metoda, nazwa, CRC-32 albo rozmiar | `zip-local-header-mismatch` | `archive/zip` (ufa katalogowi); programy czytające strumieniem ufają nagłówkowi lokalnemu, więc widzą inną treść | APPNOTE 4.3.7 i 4.3.12 opisują ten sam plik dwa razy; ADR 0001 i ADR 0003 pkt 4. Test: `TestLocalHeaderFields`, pliki `incons-local-*` i `incons-central-*` z libzip |
| dwa wpisy dzielące bajty pliku, także dwa wpisy katalogu z jednym nagłówkiem lokalnym | `zip-entries-overlap` | `archive/zip` | tak zbudowana jest bomba o nakładających się wpisach (`zbsm.zip`: 250 wpisów, jeden strumień); ADR 0003 pkt 2. Test: `TestOverlap` |
| bajty przed katalogiem, które nie należą do żadnego wpisu: dane przed pierwszym wpisem, luki między wpisami, wpis usunięty z katalogu | `zip-unreferenced-data` | `archive/zip`, większość programów | treść, której lista wpisów nie pokazuje; ADR 0003 pkt 2 i 7. Test: `TestUnreferencedData` | dane przed pierwszym wpisem: `Options.AllowPrefix` (widoczne w `Relaxed()`); luk nie przyjmuje żadna opcja |
| deskryptor danych, którego nie ma, który ma inną długość niż 12, 16, 20 albo 24 bajty albo inne wartości niż katalog | `zip-descriptor-mismatch` | `archive/zip` | APPNOTE 4.3.9; postać deskryptora wynika z długości obszaru, nie ze zgadywania (ADR 0003 pkt 3). Test: `TestDataDescriptor` |
| pola dodatkowe nagłówka lokalnego o złej strukturze (np. jeden bajt) | `zip-extra-field` | `archive/zip` | APPNOTE 4.5.1; trzy pliki `*HasEntities.ooxml` z danych testowych Apache POI mają jednobajtowe pole w nagłówku lokalnym |
| dane skompresowane wpisu dłuższe niż strumień DEFLATE | `inflate-trailing-data` (przy odczycie) | `archive/zip` | bajty wpisu, które nie są jego treścią; ADR 0003 pkt 10. Test: `TestContentErrors` |
| wpis Stored o różnych rozmiarach | `zip-size-mismatch` | `archive/zip` (błąd dopiero przy odczycie albo wcale) | APPNOTE 4.4.8, 4.4.9. Test: `TestStoredSize` |

## Miejsca, w których przyjmujemy więcej, niż mówi litera specyfikacji

| Cecha | Reguła APPNOTE | Co robimy | Powód |
|---|---|---|---|
| plik o rozmiarze zero z niepustymi danymi skompresowanymi | 4.3.8: „Zero-byte files … MUST NOT include file data” | przyjmujemy (wiersz `cdh.empty-file-with-data` inwentarza); wpis katalogu z danymi odrzucamy | Python `zipfile` (2 bajty) i Go `archive/zip` (5 bajtów) zapisują pusty plik metodą Deflate jako pusty strumień: `python-deflated.zip`, `go-deflate.zip` w korpusie. Treść sprawdzi `Open` (etap 2) |

| pole dodatkowe ZIP64 w postaci pełnej (także pola, do których nagłówek nie odsyła) | 4.5.3: pola „MUST only appear if the corresponding … field is set to 0xFFFF or 0xFFFFFFFF” | przyjmujemy, gdy każde odczytanie daje te same wartości (wiersz `extra.0x0001.full-form`, `TestZip64ExtraFullForm`); inaczej `zip64-inconsistent` | archiwum `test/success/zip64.zip` z zestawu yauzl (`testdata/sources.json`) |
| deskryptor danych o polach 8-bajtowych przy wpisie bez ZIP64 i 4-bajtowych przy wpisie ZIP64 | 4.3.9.2: pola 8-bajtowe „when compressing files in ZIP64 format” | przyjmujemy, gdy wartości równają się katalogowi (wiersz `dd.signature`, `TestDataDescriptor`) | postać wynika z długości obszaru, więc nie ma drugiego odczytania; programy zapisują to różnie |
| pole ZIP64 w nagłówku lokalnym obok pól rozmiaru bez wartości granicznej | 4.5.3 | przyjmujemy, gdy wartości są równe (`TestLocalHeaderFields`) | tak zapisuje `zipfile` Pythona z wymuszonym ZIP64 (`python-zip64.zip`) |
| wersja wymagana oraz data i czas w nagłówku lokalnym inne niż w katalogu | 4.4.3, 4.4.6 | nie porównujemy (wiersze `lfh.version-needed`, `lfh.mod-time`, `lfh.mod-date`) | nie wpływają na odczyt; Info-ZIP z `-fz` zapisuje w nagłówku lokalnym wersję 4.5, a w katalogu 1.0 albo 2.0 (`infozip-zip64.zip`) |

## Co pokazały zestawy pobrane (2026-11-27)

568 plików z dziewięciu zestawów (`testdata/sources.json`): bez paniki, każdy błąd ze stałym kodem, a każde
archiwum przyjęte ściśle ma w `archive/zip` tę samą listę nazw i, dla wpisów czytanych bez błędu, te same bajty
treści. Werdykt każdego pliku (ściśle oraz z `UnflaggedNames: UTF8` i `AllowPrefix`): `testdata/verdicts/fetched.tsv`
(`TestFetchedArchives`). Ściśle przyjętych jest 347, w tym 5 z błędem przy odczycie treści któregoś wpisu.
Zestawy służą rozwojowi; to nie jest porównanie z konkurencją (K3 mierzy etap 3 według `methodology.md`).
Najważniejsze przypadki, w których `archive/zip` (Go 1.26.6) przyjmuje, a `gozipgo` odrzuca:

| Plik | Kod | Co to jest |
|---|---|---|
| `poi-opc/test-data/document/unicode-path.docx` | `name-unicode-path` | dokument Worda, w którym pola Unicode Path zamieniają nazwy `word/document.xml` i pliku dodatkowego: program czytający Unicode Path widzi inną treść dokumentu niż program, który go nie czyta. Dokładnie przypadek z ADR 0002 pkt 8 |
| `corkami-zip/zip/unicode.zip` | `name-unicode-path` | Unicode Path użyty zgodnie z przeznaczeniem: nazwa w nagłówku to `dg`, w polu emoji. Odrzucamy z tego samego powodu: dwa odczytania nazwy |
| `libzip-regress/…/zip-in-archive-comment.zip` | `zip-multiple-directory-ends` | całe drugie archiwum w komentarzu pierwszego |
| 55 plików `.doc` z `poi-opc` | `zip-trailing-data` | dokumenty binarne OLE z osadzonym pakietem ZIP: `archive/zip` znajduje w nich archiwum, my wymagamy, żeby plik kończył się rekordem końca katalogu |
| `libzip-regress/…/extra_field_align_*.zip` (12 plików) | `zip-extra-field` | 1–3 bajty dopełnienia po ostatnim bloku pól dodatkowych; APPNOTE 4.5.1 takiej postaci nie zna |
| `cpython-zip/exe_with_zip`, `cpython-zip/exe_with_z64`, `libzip-regress/…/junk-at-start.zip` | `zip-directory-out-of-bounds`, `zip64-inconsistent` | dane przed archiwum (pliki samorozpakowujące), z przesunięciami liczonymi od końca danych wstępnych. Z `AllowPrefix` wszystkie trzy są czytane, także `exe_with_z64`, w którym lokalizator ZIP64 wskazuje położenie liczone bez danych wstępnych |
| `libzip-regress/…/incons-*.zip` (ok. 60 plików) | różne | zestaw niespójności libzip: katalog i nagłówek lokalny mówią co innego, luki, nachodzenie, sprzeczne deskryptory, powtórzone pola ZIP64. Od etapu 2 odrzucamy wszystkie poza dwoma, w których wada jest w samej treści: `incons-data.zip` daje `zip-checksum` przy odczycie |
| `fifield-zipbomb/zbsm.zip` | `zip-entries-overlap` | bomba o nakładających się wpisach: 250 wpisów katalogu, jeden strumień. Odrzucana w `New`, przed dekompresją czegokolwiek |
| `yauzl/test/failure/*.zip` | `zip-local-header-mismatch`, `inflate-code-lengths` | przypadki „failure” yauzl dotyczące nagłówków lokalnych odrzuca `New`; dwa o wadliwych kodach Huffmana (`invalid code lengths set`, `too many length or distance symbols`) dają błąd przy odczycie |
| `libzip-regress/…/gap*.zip` | `zip-unreferenced-data` | archiwa z lukami po usuniętych wpisach (ADR 0003 pkt 7) |

## Korpus zgodności: wyjątki (K2)

Stan z 2026-11-27 (lista wpisów i treść: SHA-256 każdego wpisu z manifestu). Każde z tych archiwów jest odrzucane w trybie ścisłym kodem
`name-encoding` i czytane zgodnie z manifestem z opcją `UnflaggedNames` podaną w polu `expect` manifestu: `UTF8`
dla archiwów z macOS, `CodePage` z tablicą CP852 dla archiwum 7-Zipa z polskiego Windows.

| Archiwum | Program i wersja | Powód |
|---|---|---|
| `infozip-default.zip`, `infozip-stored.zip`, `infozip-best.zip`, `infozip-comment.zip`, `infozip-stream.zip`, `infozip-zip64.zip`, `infozip-noextra.zip` | Info-ZIP Zip 3.0 (July 5th 2008) z modyfikacjami Apple, macOS 27.0.1 | nazwa „zażółć gęślą jaźń.txt” zapisana w UTF-8 bez bitu 11 i bez pola Unicode Path |
| `macos-ditto.zip`, `macos-ditto-rsrc.zip` | `ditto` z macOS 27.0.1 | to samo |
| `windows11-7zip.zip` | 7-Zip na polskim Windows 11 (wersja niezapisana) | nazwy z polskimi literami zapisane w stronie OEM systemu (CP852) bez bitu 11; nazwa w UTF-8 jest tylko w polu Unicode Path nagłówka lokalnego, katalog centralny jej nie ma. Jako CP437 archiwum też się czyta, ale daje inne znaki („Agresêaw”). `TestSameFolderTwoWriters` porównuje wynik z archiwum Eksploratora tego samego folderu |

## Dekompresja względem `compress/flate`

Strumienie, dla których werdykt (poprawny albo błędny) różni się, z odwołaniem do RFC 1951. Test różnicowy
i `FuzzInflate` pilnują, żeby strumień przyjęty przez nas przyjmował też `compress/flate` (Go 1.26.6) z tym samym
wynikiem; różnice w drugą stronę zapisujemy tutaj.

| Strumień | `gozipgo` | `compress/flate` | Reguła |
|---|---|---|---|
| blok dynamiczny, którego kod znaków ma tylko kod końca bloku (jeden kod o długości 1) | `inflate-code-lengths` | przyjmuje, wynik pusty | RFC 1951, 3.2.7 dopuszcza niezupełny kod tylko dla odległości; ADR 0003 pkt 10. Test: `TestDynamicCodes` („single literal code”) |
