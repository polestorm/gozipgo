# Wymagania: inwentarz formatu

Status: przyjęte

Źródło: APPNOTE.TXT 6.3.10 (wersja i suma przypięte w `../docs/provenance.md`). Numery w kolumnie „APPNOTE” to
rozdziały tego tekstu. Przegląd z tekstem specyfikacji: 2026-11-26 (etap 0); decyzje o werdyktach: ADR 0002 (katalog) i ADR 0003
(nagłówek lokalny, deskryptor, dane; etap 2, 2026-11-27).

| Id | Wymaganie |
|---|---|
| FMT-001 | Każdy rekord APPNOTE (nagłówek lokalny, nagłówek szyfrowania, dane, deskryptor danych, nagłówek odszyfrowania archiwum, rekord danych dodatkowych archiwum, nagłówek katalogu centralnego, podpis cyfrowy, koniec katalogu ZIP64 w wersji 1 i 2, lokalizator ZIP64, koniec katalogu centralnego, znaczniki archiwum dzielonego) ma wiersz w inwentarzu z werdyktem. Rekord poza zakresem wykryty w archiwum daje stały kod `unsupported-*` albo `zip-*`, nigdy pominięcie. |
| FMT-002 | Każde pole każdego rekordu w zakresie ma wiersz: co daje wartość niedozwolona albo sprzeczna (`reject` z kodem) albo że pole jest wydawane wywołującemu bez interpretacji (`expose`). |
| FMT-003 | Każdy z 16 bitów flag ogólnych (4.4.4) ma werdykt. Bity 0 i 6 (szyfrowanie) oraz 5 (dane łatek): wpis jest na liście z `Unsupported`. Bity 1–2 (opcje metody), 3 (deskryptor danych) i 11 (UTF-8): przyjmowane. Bit 13 (maskowanie nagłówków lokalnych przy szyfrowanym katalogu): `unsupported-encryption` dla archiwum. Bit 4 (zarezerwowany dla rozszerzonego Deflate), bity 7–10 (nieużywane) oraz 12, 14 i 15 (zarezerwowane): `unsupported-flag` dla archiwum, bo znaczenia bitu nie znamy, więc nie wiemy, czy lista wpisów jest tym, co zapisał autor. |
| FMT-004 | Każda metoda kompresji z listy APPNOTE 4.4.5 ma werdykt: 0 i 8 obsługiwane, każda inna wartość (także spoza listy) daje wpis na liście z `Unsupported` równym `unsupported-method`; ten kod zwróci `Open` (etap 2). |
| FMT-005 | Pola dodatkowe (4.5.1): obszar pól musi być ciągiem bloków (identyfikator, długość, dane) wypełniającym zadeklarowaną długość dokładnie; inaczej `zip-extra-field`. Identyfikator 0x0001 (ZIP64) jest interpretowany (DIR-008), 0x7075 (Unicode Path) sprawdzany (NAME-008); 0x0017 (silne szyfrowanie), 0x9901 (AE-x) i 0x000f (opis łatki) dają wpis z `Unsupported`; pozostałe z list APPNOTE 4.5.2 i 4.6.1 oraz nieznane są wydawane jako surowe bajty w `Entry.Extra`. |
| FMT-006 | „Version needed to extract” (4.4.3) powyżej 4.5 daje wpis z `Unsupported` równym `unsupported-version`; w rekordzie końca katalogu ZIP64 odrzuca archiwum tym kodem. Wartości do 4.5 są przyjmowane; cechy, które oznaczają, mają własne wiersze. |
| FMT-007 | Test inwentarza (`TestFormatInventory`) czyta tabele z tego pliku i dla każdego wiersza z etapu już wykonanego buduje archiwum z daną cechą i sprawdza zapisany werdykt. Wiersz bez przypadku testowego, przypadek bez wiersza i wiersz bez werdyktu są błędami testu. Wiersze etapu jeszcze niewykonanego test liczy jako oczekujące. |

## Jak czytać tabele

Każdy wiersz inwentarza ma klucz w odwrotnych apostrofach, rozdział APPNOTE, opis, werdykt i etap, w którym werdykt
jest sprawdzany testem. Werdykty:

| Werdykt | Znaczenie |
|---|---|
| `accept` | archiwum z tą cechą jest przyjmowane, a cecha jest obsłużona zgodnie z opisem |
| `expose` | wartość jest wydawana wywołującemu tak, jak zapisano, bez interpretacji i bez wpływu na wynik |
| `reject KOD` | `New` odrzuca archiwum tym kodem (opis mówi, przy jakiej wartości) |
| `entry KOD` | archiwum jest przyjmowane, wpis jest na liście, a `Entry.Unsupported` ma ten kod; zwróci go `Open` |

Wiersze z etapem 2 dotyczą nagłówków lokalnych, deskryptorów i treści: werdykt jest planem, a kody
`zip-local-header-mismatch`, `zip-descriptor-mismatch` i `zip-unreferenced-data` powstaną razem z kodem etapu 2.

## Rekordy (4.3.6)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `record.local-header` | 4.3.7 | nagłówek lokalny przed danymi każdego wpisu; porównywany z katalogiem | `accept` | 2 |
| `record.encryption-header` | 4.3.6, 6.1 | nagłówek szyfrowania przed danymi wpisu z bitem 0 | `entry unsupported-encryption` | 1 |
| `record.file-data` | 4.3.8 | dane wpisu | `accept` | 2 |
| `record.data-descriptor` | 4.3.9 | deskryptor danych po danych wpisu z bitem 3 | `accept` | 2 |
| `record.archive-decryption-header` | 4.3.10, 7.3.3 | nagłówek odszyfrowania archiwum (szyfrowany katalog; zawsze z rekordem ZIP64 w wersji 2) | `reject unsupported-version` | 1 |
| `record.archive-extra-data` | 4.3.11 | rekord danych dodatkowych archiwum na początku katalogu (0x08064b50) | `reject unsupported-encryption` | 1 |
| `record.central-header` | 4.3.12 | nagłówek katalogu centralnego: jedyne źródło listy wpisów | `accept` | 1 |
| `record.digital-signature` | 4.3.13 | podpis cyfrowy katalogu (0x05054b50) po ostatnim nagłówku | `reject unsupported-digital-signature` | 1 |
| `record.zip64-end` | 4.3.14 | koniec katalogu ZIP64, wersja 1 | `accept` | 1 |
| `record.zip64-end-v2` | 4.3.14.2, 7.3.4 | koniec katalogu ZIP64, wersja 2 (katalog skompresowany albo szyfrowany; wersja wymagana 6.2) | `reject unsupported-version` | 1 |
| `record.zip64-extensible-data` | 4.3.14.3, 4.4.27, dodatek C | sektor danych rozszerzalnych w rekordzie ZIP64 | `reject unsupported-zip64-data` | 1 |
| `record.zip64-locator` | 4.3.15 | lokalizator końca katalogu ZIP64 | `accept` | 1 |
| `record.end` | 4.3.16 | koniec katalogu centralnego | `accept` | 1 |
| `record.end.absent` | 4.3.1 | plik bez rekordu końca katalogu | `reject zip-no-directory-end` | 1 |
| `record.end.second` | 4.3.1 | drugi rekord, który też kończy się równo z plikiem („MUST have only one”) | `reject zip-multiple-directory-ends` | 1 |
| `record.end.only` | 4.3.1 | sam rekord końca katalogu: puste archiwum | `accept` | 1 |
| `record.spanning-marker` | 8.5.3, 8.5.4 | znacznik archiwum dzielonego (0x08074b50 albo 0x30304b50) przed pierwszym nagłówkiem lokalnym | `reject zip-unreferenced-data` | 2 |

## Nagłówek lokalny (4.3.7) i deskryptor danych (4.3.9)

Czytane w etapie 2. Każde pole jest porównywane z katalogiem centralnym (ENT-002, ENT-003, NAME-006).

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `lfh.signature` | 4.3.7 | sygnatura inna niż 0x04034b50 pod przesunięciem z katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.version-needed` | 4.4.3 | wersja wymagana inna niż w katalogu: nie jest porównywana, bo nie wpływa na odczyt (Info-ZIP z `-fz` zapisuje w nagłówku lokalnym 4.5, a w katalogu 1.0 albo 2.0); o zakresie decyduje wartość z katalogu | `accept` | 2 |
| `lfh.flags` | 4.4.4 | flagi istotne dla odczytu inne niż w katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.method` | 4.4.5 | metoda inna niż w katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.mod-time` | 4.4.6 | czas inny niż w katalogu (bez wpływu na treść; decyzja w etapie 2) | `accept` | 2 |
| `lfh.mod-date` | 4.4.6 | data inna niż w katalogu (jak wyżej) | `accept` | 2 |
| `lfh.crc32` | 4.4.7 | CRC-32 inne niż w katalogu (bez bitu 3) albo niezerowe i inne (z bitem 3) | `reject zip-local-header-mismatch` | 2 |
| `lfh.compressed-size` | 4.4.8 | rozmiar skompresowany inny niż w katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.uncompressed-size` | 4.4.9 | rozmiar po rozpakowaniu inny niż w katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.name-length` | 4.4.10 | długość nazwy inna niż w katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.extra-length` | 4.4.11 | pola dodatkowe wychodzące poza obszar wpisu | `reject zip-local-header-mismatch` | 2 |
| `lfh.name` | 4.4.17 | nazwa inna niż w katalogu | `reject zip-local-header-mismatch` | 2 |
| `lfh.extra` | 4.4.28, 4.5.1 | pola dodatkowe nagłówka lokalnego o złej strukturze | `reject zip-extra-field` | 2 |
| `lfh.extra.0x0001` | 4.5.3 | pole ZIP64 nagłówka lokalnego: brak przy wartości granicznej rozmiaru, długość inna niż dwa rozmiary, powtórzenie albo wartość inna niż pole rozmiaru bez wartości granicznej | `reject zip64-inconsistent` | 2 |
| `lfh.extra.0x7075` | 4.6.9 | pole Unicode Path nagłówka lokalnego, które nie daje nazwy wpisu (7-Zip na Windows zapisuje je tylko w nagłówku lokalnym) | `reject name-unicode-path` | 2 |
| `lfh.extra.encryption` | 4.5.12, dodatek E | pole silnego szyfrowania, AE-x albo opisu łatki w nagłówku lokalnym wpisu, którego katalog nie oznacza jako zaszyfrowany ani łatany | `reject zip-local-header-mismatch` | 2 |
| `dd.signature` | 4.3.9.3 | deskryptor z sygnaturą 0x08074b50 i bez niej | `accept` | 2 |
| `dd.crc32` | 4.3.9 | CRC-32 inne niż w katalogu | `reject zip-descriptor-mismatch` | 2 |
| `dd.sizes-32` | 4.3.9 | rozmiary 4-bajtowe inne niż w katalogu | `reject zip-descriptor-mismatch` | 2 |
| `dd.sizes-64` | 4.3.9.1, 4.3.9.2 | rozmiary 8-bajtowe (ZIP64) inne niż w katalogu | `reject zip-descriptor-mismatch` | 2 |

## Nagłówek katalogu centralnego (4.3.12)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `cdh.signature` | 4.3.12 | sygnatura inna niż 0x02014b50 tam, gdzie ma być nagłówek | `reject zip-directory-signature` | 1 |
| `cdh.version-made-by` | 4.4.2 | system i wersja programu zapisującego | `expose` | 1 |
| `cdh.version-needed` | 4.4.3 | wersja wymagana (werdykty wartości: tabela wersji) | `expose` | 1 |
| `cdh.flags` | 4.4.4 | flaga ogólna (werdykty bitów: tabela flag) | `expose` | 1 |
| `cdh.method` | 4.4.5 | metoda kompresji (werdykty wartości: tabela metod) | `expose` | 1 |
| `cdh.mod-time` | 4.4.6 | czas MS-DOS, rozłożony na pola bez strefy czasowej (DIR-010) | `expose` | 1 |
| `cdh.mod-date` | 4.4.6 | data MS-DOS, rozłożona na pola (DIR-010) | `expose` | 1 |
| `cdh.mod-date.invalid` | 4.4.6 | data spoza kalendarza (miesiąc 0): wydawana bez poprawiania, `Valid` fałszywe | `accept` | 1 |
| `cdh.crc32` | 4.4.7 | CRC-32 treści; sprawdzane przy odczycie w etapie 2 | `expose` | 1 |
| `cdh.compressed-size` | 4.4.8 | rozmiar skompresowany; 0xFFFFFFFF odsyła do pola ZIP64 | `expose` | 1 |
| `cdh.uncompressed-size` | 4.4.9 | rozmiar po rozpakowaniu; 0xFFFFFFFF odsyła do pola ZIP64 | `expose` | 1 |
| `cdh.name-length` | 4.4.10 | nazwa wychodząca poza koniec katalogu | `reject zip-directory-truncated` | 1 |
| `cdh.extra-length` | 4.4.11 | pola dodatkowe wychodzące poza koniec katalogu | `reject zip-directory-truncated` | 1 |
| `cdh.comment-length` | 4.4.12 | komentarz wychodzący poza koniec katalogu | `reject zip-directory-truncated` | 1 |
| `cdh.disk-start` | 4.4.13 | numer dysku inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `cdh.internal-attrs` | 4.4.14 | atrybuty wewnętrzne | `expose` | 1 |
| `cdh.external-attrs` | 4.4.15 | atrybuty zewnętrzne (zależne od systemu z „version made by”) | `expose` | 1 |
| `cdh.local-offset` | 4.4.16 | przesunięcie nagłówka lokalnego; sprawdzane z plikiem w etapie 2 | `expose` | 1 |
| `cdh.name` | 4.4.17 | nazwa wpisu, ze ścieżką względną i separatorem „/” (NAME-001…005) | `accept` | 1 |
| `cdh.name.empty` | 4.4.10 | nazwa o długości zero (wpis ze standardowego wejścia) | `reject name-empty` | 1 |
| `cdh.name.leading-slash` | 4.4.17.1 | nazwa zaczynająca się od „/” („MUST NOT”) | `reject name-absolute` | 1 |
| `cdh.name.drive-letter` | 4.4.17.1 | nazwa z literą dysku („MUST NOT”) | `reject name-absolute` | 1 |
| `cdh.name.backslash` | 4.4.17.1 | odwrotny ukośnik w nazwie („MUST be forward slashes”) | `reject name-backslash` | 1 |
| `cdh.name.unflagged-non-ascii` | dodatek D.2 | bajty spoza ASCII w nazwie bez bitu 11: kodowanie nieokreślone w praktyce | `reject name-encoding` | 1 |
| `cdh.extra` | 4.4.28, 4.5.1 | pola dodatkowe o poprawnej strukturze | `expose` | 1 |
| `cdh.extra.malformed` | 4.5.1 | blok pól dodatkowych wychodzący poza ich obszar | `reject zip-extra-field` | 1 |
| `cdh.comment` | 4.4.18 | komentarz wpisu; z bitem 11 musi być UTF-8, poza tym nieinterpretowany | `expose` | 1 |
| `cdh.comment.not-utf8` | 4.4.4 (bit 11), dodatek D.2 | komentarz wpisu z bitem 11, który nie jest UTF-8 | `reject zip-comment-encoding` | 1 |
| `cdh.directory-with-data` | 4.3.8 | wpis katalogu (nazwa zakończona „/”) z niezerowym rozmiarem („MUST NOT include file data”) | `reject zip-directory-entry-size` | 1 |
| `cdh.empty-file-with-data` | 4.3.8 | plik o rozmiarze zero z niepustym strumieniem Deflate (odstępstwo od 4.3.8, `docs/differences.md`) | `accept` | 1 |

## Koniec katalogu ZIP64 (4.3.14) i lokalizator (4.3.15)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `z64.signature` | 4.3.14 | sygnatura inna niż 0x06064b50 tam, gdzie wskazuje lokalizator | `reject zip64-inconsistent` | 1 |
| `z64.record-size` | 4.3.14.1 | rozmiar, przy którym rekord nie kończy się na początku lokalizatora | `reject zip64-inconsistent` | 1 |
| `z64.version-made-by` | 4.4.2 | wersja programu zapisującego: bez znaczenia dla odczytu, niewydawana | `accept` | 1 |
| `z64.version-needed` | 4.4.3 | wersja wymagana powyżej 4.5 | `reject unsupported-version` | 1 |
| `z64.disk` | 4.4.19 | numer tego dysku inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `z64.dir-disk` | 4.4.20 | numer dysku z początkiem katalogu inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `z64.entries-on-disk` | 4.4.21 | liczba wpisów na tym dysku inna niż łączna | `reject unsupported-multi-volume` | 1 |
| `z64.entries` | 4.4.22 | liczba wpisów inna niż w zwykłym rekordzie, który nie ma wartości granicznej | `reject zip64-inconsistent` | 1 |
| `z64.dir-size` | 4.4.23 | rozmiar katalogu inny niż w zwykłym rekordzie bez wartości granicznej | `reject zip64-inconsistent` | 1 |
| `z64.dir-offset` | 4.4.24 | przesunięcie katalogu inne niż w zwykłym rekordzie bez wartości granicznej | `reject zip64-inconsistent` | 1 |
| `loc.signature` | 4.3.15, 4.4.1.4 | brak lokalizatora, gdy zwykły rekord ma wartość graniczną | `reject zip64-inconsistent` | 1 |
| `loc.disk` | 4.3.15 | numer dysku z rekordem ZIP64 inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `loc.offset` | 4.3.15 | przesunięcie, pod którym nie ma rekordu ZIP64 przed lokalizatorem | `reject zip64-inconsistent` | 1 |
| `loc.disks` | 4.3.15 | łączna liczba dysków inna niż 1 | `reject unsupported-multi-volume` | 1 |
| `loc.unneeded` | 4.3.9.2 | rekordy ZIP64 bez wartości granicznych w zwykłym rekordzie, w pełni z nim zgodne | `accept` | 1 |

## Koniec katalogu centralnego (4.3.16)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `end.signature` | 4.3.16 | brak sygnatury 0x06054b50 w ostatnich 22 + 65 535 bajtach | `reject zip-no-directory-end` | 1 |
| `end.disk` | 4.4.19 | numer tego dysku inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `end.dir-disk` | 4.4.20 | numer dysku z początkiem katalogu inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `end.entries-on-disk` | 4.4.21 | liczba wpisów na tym dysku inna niż łączna | `reject unsupported-multi-volume` | 1 |
| `end.entries` | 4.4.22 | liczba wpisów inna niż liczba nagłówków w katalogu | `reject zip-directory-count-mismatch` | 1 |
| `end.dir-size` | 4.4.23 | rozmiar, przy którym katalog nie kończy się na początku rekordów końca | `reject zip-directory-out-of-bounds` | 1 |
| `end.dir-offset` | 4.4.24 | przesunięcie, przy którym katalog nie kończy się na początku rekordów końca | `reject zip-directory-out-of-bounds` | 1 |
| `end.comment-length` | 4.4.25 | długość komentarza mniejsza niż liczba bajtów do końca pliku | `reject zip-trailing-data` | 1 |
| `end.comment` | 4.4.26 | komentarz archiwum, nieinterpretowany | `expose` | 1 |

## Flaga ogólna (4.4.4)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `flag.0` | 4.4.4 | wpis zaszyfrowany | `entry unsupported-encryption` | 1 |
| `flag.1` | 4.4.4 | opcja metody (Implode, Deflate, LZMA): bez znaczenia dla odczytu metod 0 i 8 | `accept` | 1 |
| `flag.2` | 4.4.4 | opcja metody (Implode, Deflate): bez znaczenia dla odczytu metod 0 i 8 | `accept` | 1 |
| `flag.3` | 4.4.4 | CRC-32 i rozmiary w deskryptorze danych (czytany w etapie 2; katalog ma wartości) | `accept` | 1 |
| `flag.4` | 4.4.4 | zarezerwowany dla rozszerzonego Deflate | `reject unsupported-flag` | 1 |
| `flag.5` | 4.4.4 | skompresowane dane łatek | `entry unsupported-patch` | 1 |
| `flag.6` | 4.4.4, 7.2.2 | silne szyfrowanie | `entry unsupported-encryption` | 1 |
| `flag.7` | 4.4.4 | nieużywany | `reject unsupported-flag` | 1 |
| `flag.8` | 4.4.4 | nieużywany | `reject unsupported-flag` | 1 |
| `flag.9` | 4.4.4 | nieużywany | `reject unsupported-flag` | 1 |
| `flag.10` | 4.4.4 | nieużywany | `reject unsupported-flag` | 1 |
| `flag.11` | 4.4.4, dodatek D | nazwa i komentarz w UTF-8 | `accept` | 1 |
| `flag.12` | 4.4.4 | zarezerwowany (rozszerzona kompresja) | `reject unsupported-flag` | 1 |
| `flag.13` | 4.4.4, 7.3.2 | wartości w nagłówkach lokalnych zamaskowane (szyfrowany katalog) | `reject unsupported-encryption` | 1 |
| `flag.14` | 4.4.4 | zarezerwowany (strumienie alternatywne) | `reject unsupported-flag` | 1 |
| `flag.15` | 4.4.4 | zarezerwowany | `reject unsupported-flag` | 1 |

## Metody kompresji (4.4.5)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `method.0` | 4.4.5 | Stored | `accept` | 1 |
| `method.1` | 4.4.5, 5.1 | Shrunk | `entry unsupported-method` | 1 |
| `method.2` | 4.4.5, 5.2 | Reduced, współczynnik 1 | `entry unsupported-method` | 1 |
| `method.3` | 4.4.5, 5.2 | Reduced, współczynnik 2 | `entry unsupported-method` | 1 |
| `method.4` | 4.4.5, 5.2 | Reduced, współczynnik 3 | `entry unsupported-method` | 1 |
| `method.5` | 4.4.5, 5.2 | Reduced, współczynnik 4 | `entry unsupported-method` | 1 |
| `method.6` | 4.4.5, 5.3 | Imploded | `entry unsupported-method` | 1 |
| `method.7` | 4.4.5, 5.4 | zarezerwowana (Tokenizing) | `entry unsupported-method` | 1 |
| `method.8` | 4.4.5, 5.5 | Deflated | `accept` | 1 |
| `method.9` | 4.4.5, 5.6 | Deflate64 | `entry unsupported-method` | 1 |
| `method.10` | 4.4.5, 5.13 | PKWARE DCL Imploding | `entry unsupported-method` | 1 |
| `method.11` | 4.4.5, 5.14 | zarezerwowana | `entry unsupported-method` | 1 |
| `method.12` | 4.4.5, 5.7 | BZIP2 | `entry unsupported-method` | 1 |
| `method.13` | 4.4.5, 5.15 | zarezerwowana | `entry unsupported-method` | 1 |
| `method.14` | 4.4.5, 5.8 | LZMA | `entry unsupported-method` | 1 |
| `method.15` | 4.4.5, 5.16 | zarezerwowana | `entry unsupported-method` | 1 |
| `method.16` | 4.4.5, 5.17 | IBM z/OS CMPSC | `entry unsupported-method` | 1 |
| `method.17` | 4.4.5, 5.18 | zarezerwowana | `entry unsupported-method` | 1 |
| `method.18` | 4.4.5, 5.19 | IBM TERSE (nowy) | `entry unsupported-method` | 1 |
| `method.19` | 4.4.5, 5.20 | IBM LZ77 z Architecture | `entry unsupported-method` | 1 |
| `method.20` | 4.4.5 | wycofana (zstd) | `entry unsupported-method` | 1 |
| `method.93` | 4.4.5 | Zstandard | `entry unsupported-method` | 1 |
| `method.94` | 4.4.5 | MP3 | `entry unsupported-method` | 1 |
| `method.95` | 4.4.5 | XZ | `entry unsupported-method` | 1 |
| `method.96` | 4.4.5, 5.12 | wariant JPEG | `entry unsupported-method` | 1 |
| `method.97` | 4.4.5, 5.9 | WavPack | `entry unsupported-method` | 1 |
| `method.98` | 4.4.5, 5.10 | PPMd | `entry unsupported-method` | 1 |
| `method.99` | 4.4.5, 5.11, dodatek E | znacznik szyfrowania AE-x (zawsze z bitem 0) | `entry unsupported-encryption` | 1 |
| `method.other` | 4.4.5 | wartość spoza listy (sprawdzane: 21, 92, 100, 65535) | `entry unsupported-method` | 1 |

## Wersja wymagana (4.4.3.2)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `version.10` | 4.4.3.2 | 1.0: wartość domyślna | `accept` | 1 |
| `version.11` | 4.4.3.2 | 1.1: etykieta woluminu (wpis jak każdy inny; atrybutów nie interpretujemy) | `accept` | 1 |
| `version.20` | 4.4.3.2 | 2.0: katalog, Deflate, szyfrowanie tradycyjne (ma własny wiersz: `flag.0`) | `accept` | 1 |
| `version.21` | 4.4.3.2 | 2.1: Deflate64 (własny wiersz: `method.9`) | `accept` | 1 |
| `version.25` | 4.4.3.2 | 2.5: PKWARE DCL Implode (własny wiersz: `method.10`) | `accept` | 1 |
| `version.27` | 4.4.3.2 | 2.7: dane łatek (własny wiersz: `flag.5`) | `accept` | 1 |
| `version.45` | 4.4.3.2 | 4.5: rozszerzenia ZIP64 | `accept` | 1 |
| `version.46` | 4.4.3.2 | 4.6: BZIP2 | `entry unsupported-version` | 1 |
| `version.50` | 4.4.3.2 | 5.0: DES, 3DES, RC2, RC4 | `entry unsupported-version` | 1 |
| `version.51` | 4.4.3.2 | 5.1: AES, poprawione RC2 | `entry unsupported-version` | 1 |
| `version.52` | 4.4.3.2 | 5.2: poprawione RC2-64 | `entry unsupported-version` | 1 |
| `version.61` | 4.4.3.2 | 6.1: opakowanie klucza bez OAEP | `entry unsupported-version` | 1 |
| `version.62` | 4.4.3.2 | 6.2: szyfrowanie katalogu centralnego | `entry unsupported-version` | 1 |
| `version.63` | 4.4.3.2 | 6.3: LZMA, PPMd, Blowfish, Twofish | `entry unsupported-version` | 1 |
| `version.other` | 4.4.3.1 | każda inna wartość powyżej 4.5 (sprawdzane: 47, 64, 255, 0x0314) | `entry unsupported-version` | 1 |

## Pola dodatkowe: identyfikatory PKWARE (4.5.2)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `extra.0x0001` | 4.5.3 | ZIP64: rozmiary, przesunięcie i numer dysku dla pól z wartością graniczną (DIR-008) | `accept` | 1 |
| `extra.0x0001.unneeded` | 4.5.3 | pole ZIP64, gdy żadne pole nagłówka nie ma wartości granicznej („MUST only appear if…”) | `reject zip64-inconsistent` | 1 |
| `extra.0x0001.missing` | 4.4.8, 4.4.9, 4.4.16 | wartość graniczna w nagłówku bez pola ZIP64 | `reject zip64-inconsistent` | 1 |
| `extra.0x0001.length` | 4.5.3 | pole ZIP64 o długości innej niż suma pól, do których odsyła nagłówek, które nie jest zgodną postacią pełną | `reject zip64-inconsistent` | 1 |
| `extra.0x0001.full-form` | 4.5.3 | postać pełna: pola po kolei od pierwszego, także te, do których nagłówek nie odsyła, o wartościach równych nagłówkowi (odstępstwo od „MUST only appear”, `docs/differences.md`) | `accept` | 1 |
| `extra.0x0001.duplicate` | 4.5.3 | dwa pola ZIP64 w jednym nagłówku | `reject zip64-inconsistent` | 1 |
| `extra.0x0001.disk` | 4.4.13, 4.5.3 | numer dysku w polu ZIP64 inny niż 0 | `reject unsupported-multi-volume` | 1 |
| `extra.0x0007` | 4.5.2 | AV Info | `expose` | 1 |
| `extra.0x0008` | 4.5.2, dodatek D.3 | zarezerwowane: rozszerzone dane o kodowaniu (układ nieokreślony) | `expose` | 1 |
| `extra.0x0009` | 4.5.4 | OS/2 | `expose` | 1 |
| `extra.0x000a` | 4.5.5 | NTFS | `expose` | 1 |
| `extra.0x000c` | 4.5.6 | OpenVMS | `expose` | 1 |
| `extra.0x000d` | 4.5.7 | UNIX | `expose` | 1 |
| `extra.0x000e` | 4.5.2 | zarezerwowane: opisy strumieni i rozwidleń | `expose` | 1 |
| `extra.0x000f` | 4.5.8 | opis łatki | `entry unsupported-patch` | 1 |
| `extra.0x0014` | 4.5.9 | magazyn PKCS#7 certyfikatów X.509 | `expose` | 1 |
| `extra.0x0015` | 4.5.10 | identyfikator certyfikatu X.509 i podpis pliku | `expose` | 1 |
| `extra.0x0016` | 4.5.11 | identyfikator certyfikatu X.509 katalogu centralnego | `expose` | 1 |
| `extra.0x0017` | 4.5.12, 7.2.3 | nagłówek silnego szyfrowania | `entry unsupported-encryption` | 1 |
| `extra.0x0018` | 4.5.13 | Record Management Controls | `expose` | 1 |
| `extra.0x0019` | 4.5.14 | lista certyfikatów odbiorców szyfrowania PKCS#7 | `expose` | 1 |
| `extra.0x0020` | 4.5.2 | zarezerwowane: znacznik czasu | `expose` | 1 |
| `extra.0x0021` | 4.5.17 | Policy Decryption Key Record | `expose` | 1 |
| `extra.0x0022` | 4.5.18 | Smartcrypt Key Provider Record | `expose` | 1 |
| `extra.0x0023` | 4.5.19 | Smartcrypt Policy Key Data Record | `expose` | 1 |
| `extra.0x0065` | 4.5.15, 4.5.16 | atrybuty IBM S/390 (Z390), AS/400 (I400), nieskompresowane | `expose` | 1 |
| `extra.0x0066` | 4.5.2 | zarezerwowane: atrybuty IBM, skompresowane | `expose` | 1 |
| `extra.0x4690` | 4.5.2 | POSZIP 4690 (zarezerwowane) | `expose` | 1 |

## Pola dodatkowe: identyfikatory innych autorów (4.6.1)

| Klucz | APPNOTE | Opis | Werdykt | Etap |
|---|---|---|---|---|
| `extra.0x07c8` | 4.6.1 | Macintosh | `expose` | 1 |
| `extra.0x1986` | 4.6.1 | Pixar USD | `expose` | 1 |
| `extra.0x2605` | 4.6.4 | ZipIt Macintosh | `expose` | 1 |
| `extra.0x2705` | 4.6.5 | ZipIt Macintosh 1.3.5+ (pliki) | `expose` | 1 |
| `extra.0x2805` | 4.6.6 | ZipIt Macintosh 1.3.5+ (katalogi) | `expose` | 1 |
| `extra.0x334d` | 4.6.1 | Info-ZIP Macintosh | `expose` | 1 |
| `extra.0x4154` | 4.6.1 | Tandem | `expose` | 1 |
| `extra.0x4341` | 4.6.1 | Acorn/SparkFS | `expose` | 1 |
| `extra.0x4453` | 4.6.1 | deskryptor zabezpieczeń Windows NT | `expose` | 1 |
| `extra.0x4704` | 4.6.1 | VM/CMS | `expose` | 1 |
| `extra.0x470f` | 4.6.1 | MVS | `expose` | 1 |
| `extra.0x4854` | 4.6.1 | THEOS (stare) | `expose` | 1 |
| `extra.0x4b46` | 4.6.7 | FWKCS MD5 | `expose` | 1 |
| `extra.0x4c41` | 4.6.1 | lista kontroli dostępu OS/2 | `expose` | 1 |
| `extra.0x4d49` | 4.6.1 | Info-ZIP OpenVMS | `expose` | 1 |
| `extra.0x4d63` | 4.6.1 | Macintosh Smartzip | `expose` | 1 |
| `extra.0x4f4c` | 4.6.1 | Xceed: pierwotne położenie | `expose` | 1 |
| `extra.0x5356` | 4.6.1 | AOS/VS (ACL) | `expose` | 1 |
| `extra.0x5455` | 4.6.1 | rozszerzony znacznik czasu | `expose` | 1 |
| `extra.0x554e` | 4.6.1 | Xceed: pole Unicode | `expose` | 1 |
| `extra.0x5855` | 4.6.1 | Info-ZIP UNIX (pierwotne) | `expose` | 1 |
| `extra.0x6375` | 4.6.8 | Info-ZIP Unicode Comment (komentarzy nie interpretujemy) | `expose` | 1 |
| `extra.0x6542` | 4.6.1 | BeOS/BeBox | `expose` | 1 |
| `extra.0x6854` | 4.6.1 | THEOS | `expose` | 1 |
| `extra.0x7075` | 4.6.9 | Info-ZIP Unicode Path: wersja 1, CRC-32 pola nazwy i ta sama nazwa (NAME-008) | `accept` | 1 |
| `extra.0x7075.mismatch` | 4.6.9 | Unicode Path o innej nazwie, złym CRC-32, innej wersji albo powtórzone | `reject name-unicode-path` | 1 |
| `extra.0x7441` | 4.6.1 | AtheOS/Syllable | `expose` | 1 |
| `extra.0x756e` | 4.6.1 | ASi UNIX | `expose` | 1 |
| `extra.0x7855` | 4.6.1 | Info-ZIP UNIX (nowe) | `expose` | 1 |
| `extra.0x7875` | 4.6.1 | Info-ZIP UNIX (nowsze, UID/GID) | `expose` | 1 |
| `extra.0xa11e` | 4.6.11 | wyrównanie strumienia danych (Apache Commons-Compress) | `expose` | 1 |
| `extra.0xa220` | 4.6.10 | Microsoft Open Packaging Growth Hint | `expose` | 1 |
| `extra.0xcafe` | 4.6.1 | Java JAR | `expose` | 1 |
| `extra.0xd935` | 4.6.1 | wyrównanie ZIP w Androidzie | `expose` | 1 |
| `extra.0xe57a` | 4.6.1 | koreańska strona kodowa | `expose` | 1 |
| `extra.0xfd4a` | 4.6.1 | SMS/QDOS | `expose` | 1 |
| `extra.0x9901` | 4.6.1, dodatek E | struktura szyfrowania AE-x | `entry unsupported-encryption` | 1 |
| `extra.0x9902` | 4.6.1 | nieznane | `expose` | 1 |
| `extra.unknown` | 4.5.1, 4.6.2 | identyfikator spoza obu list (sprawdzane: 0x0000, 0x0002, 0x8000, 0xffff) | `expose` | 1 |
