# Dane testowe

| Katalog | Zawartość | Źródło | W repozytorium |
|---|---|---|---|
| `interop/` | korpus zgodności: małe archiwa utworzone prawdziwymi programami, z manifestem `interop/manifest.json` | skrypt `tools/corpus` (polecenia dla każdego programu) uruchamiany na systemie, który dany program ma | tak |
| `hostile.json` | manifest korpusu wrogiego: nazwa, opis, przepis budowniczego, oczekiwany kod | pisany ręcznie razem z wymaganiami | tak |
| `fetched/` | zestawy publiczne pobierane według `sources.json` | `tools/fetch` | nie |
| `verdicts/fetched.tsv` | werdykt czytnika dla każdego pliku z zestawów pobranych (wzorzec `TestFetchedArchives`) | `go test ./zipreader -run TestFetchedArchives -update` | tak |
| `<pakiet>/testdata/fuzz/` | wejścia regresji fuzzingu | znaleziska kampanii | tak |

## Korpus zgodności: stan z 2026-11-26

22 archiwa w `interop/`: 20 zbudowanych poleceniem `go -C tools/corpus run . -make` na macOS 27.0.1 (arm64):
Info-ZIP Zip 3.0 w wydaniu Apple (7 wariantów), 7-Zip 26.01 (4), `ditto` (2), Python 3.9.6 `zipfile` (4),
Go 1.26.6 `archive/zip` (3), oraz dwa dodane przez właściciela (`-add`), z tym samym folderem plików o polskich
nazwach spakowanym na polskim Windows 11: `windows11-explorer.zip` (Eksplorator, „Kompresuj do pliku ZIP”)
i `windows11-7zip.zip` (7-Zip). 7-Zip zapisuje nazwy spoza ASCII w stronie OEM systemu (CP852) bez bitu 11,
a nazwę w UTF-8 tylko w polu Unicode Path nagłówka lokalnego; czytnik odrzuca je ściśle i czyta z tablicą CP852
(wyjątek w `differences.md`). Archiwum Eksploratora pokazało, że Windows 11 zapisuje nazwy spoza ASCII w UTF-8 z bitem 11 (nazwy w ASCII bez bitu),
podaje „made by” 3.20 Unix, a każdy plik, także pusty, ma metodę 8 i deskryptor danych (bit 3) oraz pola
dodatkowe `UT` i `ux`; czytnik przyjmuje je ściśle. Manifest zapisuje program, wersję, system, polecenie, SHA-256 archiwum oraz nazwę,
rozmiar, CRC-32 i SHA-256 każdego wpisu; treść wpisów czyta `archive/zip` jako czarna skrzynka i porównuje
z plikami źródłowymi, które narzędzie samo generuje. `go -C tools/corpus run . -check` sprawdza zgodność archiwów
z manifestem. Ponowne `-make` nie daje tych samych bajtów (programy zapisują identyfikatory użytkownika i czasy
dostępu), więc zastępuje korpus; archiwa w repozytorium są korpusem.

**Brakuje** (programów nie ma na maszynie budującej): Java (`jar`), Microsoft Word,
LibreOffice. Właściciel dodaje je poleceniem
`go -C tools/corpus run . -add PLIK -id ID -program P -version V -system S -command C`.
Wariant „Go ZIP64” pominięto: `archive/zip` zapisuje ZIP64 tylko dla ponad 65 535 wpisów albo 4 GB, czyli poza
rozmiarem korpusu; ZIP64 pokrywają `infozip-zip64.zip` i `python-zip64.zip`.

## Zestawy pobierane (`sources.json`)

Dziewięć zestawów, pobieranych poleceniem `go -C tools/fetch run . -root ../..` do `fetched/` (poza repozytorium):
zbiory testowe yauzl, libzip, rc-zip, Go `archive/zip` i CPython, archiwa-ciekawostki corkami, próbki Zip Slip,
bomba `zbsm.zip` Fifielda i dane testowe Apache POI (pakiety OPC i dokumenty Worda). Każdy ma adres z przypiętym
commitem i sumę; z repozytoriów implementacji bierzemy wyłącznie katalogi z danymi (pole `include`), kodu nie
pobieramy na dysk (`docs/provenance.md`).

Zasada użycia (decyzja właściciela z 2026-11-26): **do rozwoju** wolno używać każdego zbioru, także bez licencji
(`corkami-zip`, `zip-slip`: opis w polu `note`); **do publikacji** (liczby w README, porównania, pakiet dowodów)
tylko zbiorów wolnych albo takich, których źródło wskazujemy tak, że każdy pobierze te same bajty. Pliki z tych
zestawów nie trafiają do repozytorium; błąd znaleziony na takim pliku dostaje test z archiwum zbudowanym naszym
budowniczym. Werdykty czytnika dla wszystkich plików trzyma `verdicts/fetched.tsv` (same ścieżki i kody).

## Korpus zgodności: lista docelowa

Treść wymyślona, te same pliki źródłowe dla każdego programu (kilka plików tekstowych, plik pusty, plik binarny
o stałej treści z generatora, podkatalog, nazwa z polskimi literami, nazwa ze spacją). Każde archiwum poniżej
kilkudziesięciu kB. Manifest podaje program, wersję, system, dokładne polecenie, SHA-256 archiwum i każdego wpisu.

| Program | Warianty |
|---|---|
| Info-ZIP `zip` | domyślny, `-0` (Stored), `-9`, z komentarzem, ze strumienia (deskryptor danych), `-fz` (wymuszone ZIP64) |
| 7-Zip | format zip, Deflate; poziomy skrajne |
| Windows (Eksplorator, „Wyślij do folderu skompresowanego”) | domyślny |
| macOS (Narzędzie archiwizacji albo `ditto -c -k`) | domyślny (z wpisami `__MACOSX`, deskryptory danych) |
| Python `zipfile` | Stored, Deflate, nazwy UTF-8, zapis strumieniowy |
| Java (`jar`, `java.util.zip`) | domyślny (pole dodatkowe 0xCAFE) |
| Go `archive/zip` | Stored, Deflate, z deskryptorem i bez, ZIP64 |
| Microsoft Word | pusty dokument `.docx` z treścią wymyśloną |
| LibreOffice Writer | to samo jako `.docx` i `.odt` |

Programy, których nie ma na maszynie budującej, uzupełnia właściciel według tej samej listy; archiwum bez wpisu
w manifeście nie wchodzi do korpusu. Archiwa spoza listy (od odbiorców, z sieci) nie wchodzą nigdy.

## Korpus wrogi

40 archiwów w 14 rodzinach z `../requirements/safety.md` (SAFE-003). Buduje je test (`hostileArchives`
w `../zipreader/hostile_test.go`), bajt w bajt tak samo na każdym systemie; repozytorium trzyma tylko manifest
`hostile.json` (opis, kod, którym czytnik odrzuca archiwum, SHA-256). Pliki dla innych programów zapisuje
`go test ./zipreader -run TestHostileArchives -write-hostile KATALOG`; `tools/compare` robi to samo i sprawdza sumy.
Zmiana korpusu wymaga `-update-hostile` i przeglądu różnicy w manifeście. Opisy ataków pochodzą z publikacji
cytowanych w `../docs/provenance.md`; archiwów z cudzych zbiorów nie kopiujemy.

## Dane prywatne: nigdy w repozytorium

Pliki odbiorców nie trafiają do repozytorium ani do porównań. Jeśli posłużą do lokalnej próby, do dokumentów
trafiają tylko liczby i kody błędów, oznaczone jako niepowtarzalne z zewnątrz.
