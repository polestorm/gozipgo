# Różnice i wyłączenia

Każdy wpis powstaje razem z testem, który różnicę pokazuje, i wskazuje regułę specyfikacji albo decyzję z `adr/`.

## Archiwa, które inne programy otwierają, a `gozipgo` w trybie ścisłym odrzuca

| Archiwum albo rodzina | Kod | Kto otwiera | Powód odrzucenia | Opcja poluzowania |
|---|---|---|---|---|
| nazwa spoza ASCII zapisana w UTF-8 bez bitu 11 (`zip` i `ditto` z macOS) | `name-encoding` | `archive/zip` (wydaje nazwę z oznaczeniem `NonUTF8`); pozostałe czarne skrzynki: pomiar w etapie 3 | APPNOTE, dodatek D.2: bez bitu 11 nazwa jest w CP437, w praktyce w kodowaniu systemu; archiwum nie mówi, w którym, więc nazwa ma więcej niż jedno odczytanie (ADR 0002 pkt 6). Test: `TestInteropCorpus`, `TestUnflaggedNames` | `Options.UnflaggedNames: UTF8` albo `CP437`; użycie widoczne w `Relaxed()` |
| dane przed archiwum (plik samorozpakowujący) | `zip-directory-out-of-bounds` | pomiar w etapie 3 | katalog nie kończy się tam, gdzie mówi koniec katalogu; to samo miejsce służy do przemycania treści (ADR 0001 pkt 5). Test: `TestDirectoryBounds` | planowana `AllowPrefix` (etap 2, pytanie 3) |
| sygnatura końca katalogu (`PK\x05\x06`) w komentarzu archiwum albo w polach rekordu końca | `zip-multiple-directory-ends` | 7-Zip (bierze rekord kończący się równo z plikiem); `archive/zip`, `zipfile` Pythona, UnZip i bsdtar zaczynają od ostatniej sygnatury i czytają inny katalog albo odrzucają | jeden plik, dwie zawartości: archiwum `two-ends-comment-trailing` korpusu wrogiego (ADR 0002 pkt 3, zmiana z 2026-11-27 po znalezisku `FuzzReader`). Test: `TestDirectoryEnd`, wejście regresji `testdata/fuzz/FuzzReader/5f059cbfad239190` | brak |
| „version needed to extract” z numerem systemu w starszym bajcie (np. 788 = Unix, 2.0) | `unsupported-version` (przy `Open`) | wszystkie zmierzone programy | całe pole jako liczba daje wersję ponad 4.5 (FMT-006). Test: `TestAllowVersionHostByte` | `Options.AllowVersionHostByte` (ADR 0006), widoczne w `Relaxed()` |
| nagłówek lokalny z polem ZIP64 z samych zer obok prawdziwych rozmiarów | `zip64-inconsistent` | wszystkie zmierzone programy | pole obecne musi mówić to samo co nagłówek: program, który ufa polu, przeczytałby pusty plik (ENT-002). Test: `TestAllowUnusedZip64` | `Options.AllowUnusedZip64` (ADR 0006), widoczne w `Relaxed()` |
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
| `windows11-7zip.zip` | 7-Zip 25.01 (arm64) na polskim Windows 11 Pro 25H2 (kompilacja 26200.9974) | nazwy z polskimi literami zapisane w stronie OEM systemu (CP852) bez bitu 11; nazwa w UTF-8 jest tylko w polu Unicode Path nagłówka lokalnego, katalog centralny jej nie ma. Jako CP437 archiwum też się czyta, ale daje inne znaki („Agresêaw”). `TestSameFolderTwoWriters` porównuje wynik z archiwum Eksploratora tego samego folderu |

## Korpus zgodności: różnice między czarnymi skrzynkami (K3)

Stan z 2026-11-27, `tools/compare` (`comparison.md`, surowy wynik `results/compare.tsv`). `gozipgo`, `archive/zip`,
7-Zip i `zipfile` Pythona dają na wszystkich archiwach (22 w pierwszym pomiarze, 25 od `0a48ee9`) tę samą treść co manifest. Dwa programy nie (macOS). Decyzją właściciela z 2026-11-27 wada jednego wydania programu nie rozstrzyga K3 (ADR 0001, uzupełnienia).
Na Linuksie różnicy nie ma: ten sam pomiar w CI (krok informacyjny przepływu `test`, Ubuntu, UnZip 6.00 w wydaniu Debiana, 7-Zip 26.00, Python 3.14, go1.27.4; przebieg z 2026-11-27 na commicie `08f68e8`) dał 22/22 dla wszystkich czytników. Wynik z CI nie jest zapisany w `results/`, więc nie jest pomiarem publikowanym; pokazuje tylko, że 18/22 to cecha wydania Apple.

| Program | Archiwa | Co się dzieje | Czyja to cecha |
|---|---|---|---|
| Info-ZIP UnZip 6.00 w wydaniu Apple (macOS 27.0.1) | `go-deflate.zip`, `go-raw.zip`, `go-stored.zip`, `windows11-7zip.zip` | kończy pracę kodem 50 („write error”) na pliku o nazwie z polskimi literami: w archiwach Go nazwa jest w UTF-8 z bitem 11, w archiwum 7-Zipa w CP852; pozostałe pliki rozpakowuje poprawnie | programu: to wydanie nie zamienia takiej nazwy na poprawną nazwę pliku w systemie APFS. Treść, którą czytamy my, jest zgodna z manifestem |
| bsdtar 3.5.3 (libarchive 3.7.4, macOS) | `macos-ditto-rsrc.zip` | daje 8 plików zamiast 10: wpisy `__MACOSX/._*` scala z plikami, które opisują (atrybuty rozszerzone macOS) | programu i systemu: tak libarchive traktuje pliki AppleDouble na macOS. My wydajemy je jako zwykłe wpisy, tak jak są zapisane |

## Dekompresja względem `compress/flate`

Strumienie, dla których werdykt (poprawny albo błędny) różni się, z odwołaniem do RFC 1951. Test różnicowy
i `FuzzInflate` pilnują, żeby strumień przyjęty przez nas przyjmował też `compress/flate` (Go 1.26.6) z tym samym
wynikiem; różnice w drugą stronę zapisujemy tutaj.

| Strumień | `gozipgo` | `compress/flate` | Reguła |
|---|---|---|---|
| blok dynamiczny, którego kod znaków ma tylko kod końca bloku (jeden kod o długości 1) | `inflate-code-lengths` | przyjmuje, wynik pusty | RFC 1951, 3.2.7 dopuszcza niezupełny kod tylko dla odległości; ADR 0003 pkt 10. Test: `TestDynamicCodes` („single literal code”) |

## Archiwa zapisane przez `zipwriter` w innych programach (ZW-007)

Stan z 2026-11-27, `tools/compare` (`comparison.md`, surowy wynik `results/compare.tsv`), macOS. `archive/zip`,
7-Zip, `zipfile` Pythona i bsdtar oddają treść wszystkich 7 archiwów.

| Program | Archiwum | Co się dzieje | Czyja to cecha |
|---|---|---|---|
| Info-ZIP UnZip 6.00 w wydaniu Apple (macOS 27.0.1) | `utf8-names` | kończy pracę kodem 50 na plikach o nazwach spoza ASCII zapisanych w UTF-8 z bitem 11 | programu: ta sama różnica co dla archiwów Go i 7-Zipa w korpusie zgodności (wyżej) |
| Info-ZIP UnZip 6.00 | `empty` | kod 1 (ostrzeżenie „zipfile is empty”), nic do rozpakowania | programu: plik złożony tylko z rekordu końca katalogu jest pustym archiwum (APPNOTE 4.3.1); ostrzeżenie nie zmienia wyniku |

## Kompresja względem `compress/flate`

| Dane | `gozipgo` | `compress/flate` | Powód |
|---|---|---|---|
| jeden powtarzany bajt (32 MiB zer) | 39 058 bajtów, około 860:1 | 32 750 bajtów, około 1025:1 | blok kompresora ma najwyżej 65 535 bajtów wejścia, więc stosunek nie przekracza 1000:1 i wynik mieści się w domyślnym `MaxRatio` czytnika (ADR 0005 pkt 3) |

