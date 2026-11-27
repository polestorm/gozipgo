# Dziennik fuzzingu

Kryterium K5 (ADR 0001): fuzzery bez paniki i bez naruszenia niezmienników przez co najmniej 24 h łącznie na
kandydacie do wydania. Kampanię prowadzi `tools/fuzz` (przeniesione z rodziny w etapie 0); każdy przebieg dopisuje
tu jeden wiersz.

## Gdzie wolno uruchamiać

1. **Synology „StormSyn”** (DS224+, x86_64, 4 rdzenie Celeron J4125): wybór domyślny. Na serwerze nie ma Go:
   binarki testów buduje się na Macu (`GOOS=linux GOARCH=amd64 go test -c`), kopiuje z `testdata/fuzz` i danymi
   startowymi do `~/fuzz/gozipgo/<commit>/` i zleca przez wspólną kolejkę `~/fuzz/queue.sh` (jedna kampania naraz
   dla wszystkich projektów, `-test.parallel` najwyżej 4).
2. **Mac tylko w nocy (00:00–07:00) i na zasilaniu sieciowym**, z `-fuzztime` dobranym tak, żeby skończyć przed 07:00.
3. W pozostałych przypadkach kampania czeka w kolejce.

Lokalnie zawsze wolno: `go test` z wejściami regresji (bez `-fuzz`) oraz krótki przebieg sprawdzający poprawkę
(≤ 2 min, `-parallel 2`).

## Narzędzie

```sh
go -C tools/fuzz run . -root ../.. -list                       # fuzzery znalezione w module
go -C tools/fuzz run . -root ../.. -pack ../../out/fuzzpack    # binarki linux/amd64, run.sh, out/fuzzpack.tar.gz
tar czf - -C out/fuzzpack . | ssh <serwer> 'mkdir -p ~/fuzz/gozipgo/<commit> && tar xzf - -C ~/fuzz/gozipgo/<commit>'
ssh <serwer> 'nohup setsid ~/fuzz/queue.sh ~/fuzz/gozipgo/<commit> >/dev/null 2>&1 &'
go -C tools/fuzz run . -root ../.. -import journal.tsv         # wiersze z serwera do tabeli „Przebiegi”
go -C tools/fuzz run . -root ../.. -only Reader -duration 2m -parallel 2   # tylko krótkie sprawdzenie lokalne
```

Do K5 liczą się wiersze tabeli „Przebiegi” z wynikiem „ok”, z czystego drzewa, na commicie kandydata albo na
commicie o tym samym kodzie biblioteki; wiersz z awarią na kodzie kandydata zeruje licznik (`tools/evidence`).

Uwaga z etapu 1: przy krótkim przebiegu `go test -fuzz` z domyślnym `-fuzzminimizetime` (60 s) oba wątki potrafią
stanąć na minimalizacji nowego wejścia i licznik wykonań pokazuje 0/s; to nie zawieszenie czytnika. Krótkie
sprawdzenie lokalne uruchamiamy z `-fuzzminimizetime 1s`.

## Fuzzery i niezmienniki

| Fuzzer | Pakiet | Niezmiennik | Od etapu |
|---|---|---|---|
| `FuzzReader` | `zipreader` | bez paniki; błąd to `*Error` ze znanym kodem i przesunięciem w pliku; archiwum przyjęte ma poprawne i niekolidujące nazwy, a dane każdego wpisu leżą w pliku; każdy `Open` daje dokładnie zadeklarowaną liczbę bajtów z zadeklarowanym CRC-32 albo błąd ze znanym kodem, nigdy więcej bajtów, niż zadeklarowano; archiwum przyjęte w trybie ścisłym `archive/zip` przyjmuje z tą samą listą nazw i tymi samymi bajtami treści; wynik nie zależy od podziału odczytów. Limity treści 1 MiB na wpis i 4 MiB łącznie trzymają czas wykonania | 1 (katalog), 2 (treść i wyrocznia) |
| `FuzzInflate` | `internal/inflate` | bez paniki; błąd to `*inflate.Error`; strumień przyjęty przez nas przyjmuje też `compress/flate` z tymi samymi bajtami; bajty wydane przed naszym błędem są początkiem tego, co daje `compress/flate` | 2 |
| `FuzzRoundTrip` | `zipwriter` | z dowolnych danych powstają nazwy i treść wpisów (`Add` i `Create`, Stored i Deflate, z `Header.Zip64` i bez): błąd pisarza to `*Error`; to, co zapisał, czytnik w trybie ścisłym i `archive/zip` oddają z tymi samymi nazwami i treścią; te same wywołania z treścią podzieloną inaczej dają te same bajty | 4 |
| `FuzzDeflate` | `internal/deflate` | dowolne dane, powielone ponad długość okna i bloku: własna dekompresja i `compress/flate` oddają dane wejściowe i zużywają strumień co do bajta; wynik nie zależy od podziału na zapisy | 4 |

Wejścia startowe: korpus zgodności, korpus wrogi i archiwa z budowniczego testów (fuzzer strukturalny: mutuje
pola rekordów, nie tylko bajty).

## Znaleziska

| Data | Fuzzer | Wejście | Co pokazało | Poprawka |
|---|---|---|---|---|
| 2026-11-27 | `FuzzReader` (lokalnie, po 7 min 39 s) | `zipreader/testdata/fuzz/FuzzReader/5f059cbfad239190` | archiwum przyjęte ściśle, którego `archive/zip` nie przyjmuje: komentarz zawierał sygnaturę końca katalogu, a rekord od niej zaczęty nie kończył się równo z plikiem, więc nie był dla nas kandydatem. Zbudowany z tego przykład (drugie archiwum i jeden bajt w komentarzu) czytają inaczej niż my `archive/zip`, Python, UnZip i bsdtar. Luka była w v0.1.0 | sygnatura końca katalogu po pierwszym bajcie wybranego rekordu daje `zip-multiple-directory-ends` (DIR-002, ADR 0002 pkt 3); archiwum `two-ends-comment-trailing` w korpusie wrogim; wejście zostaje jako test regresji |

## Przebiegi

Ta tabela musi być ostatnia w pliku: `tools/fuzz` dopisuje wiersze na jego końcu.

| Data | Commit | Host i sprzęt | Fuzzer | Czas zegarowy | Wykonania | Wynik |
|---|---|---|---|---|---|---|
| — | `4a0326b` | Synology StormSyn (DS224+, Celeron J4125, 4 rdzenie) | `FuzzInflate`, `FuzzReader` | zlecone 25 h | — | nie odbyła się: zlecona 2026-11-27, czekała w kolejce, a serwer stracił zasilanie tego samego dnia. Zastąpi ją kampania czterech fuzzerów na commicie kandydata do v0.2.0 |
| 2026-11-27 10:02 | `65eabd7` (zmiany) | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzDeflate` (./internal/deflate) | 10m2s | 515549 | ok |
| 2026-11-27 10:12 | `65eabd7` (zmiany) | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzInflate` (./internal/inflate) | 10m5s | 16320196 | ok |
| 2026-11-27 10:22 | `65eabd7` (zmiany) | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzReader` (./zipreader) | 7m39s | 1572191 | **błąd**: input testdata/fuzz/FuzzReader/5f059cbfad239190 |
| 2026-11-27 10:30 | `65eabd7` (zmiany) | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzRoundTrip` (./zipwriter) | 10m2s | 1139622 | ok |
| 2026-11-27 11:02 | `28784c9` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzReader` (./zipreader) | 10m2s | 3660908 | ok |
| 2026-11-27 11:42 | `ceceeb6` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzDeflate` (./internal/deflate) | 2m32s | 41976 | ok |
| 2026-11-27 11:45 | `ceceeb6` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzInflate` (./internal/inflate) | 2m32s | 5950435 | ok |
| 2026-11-27 11:47 | `ceceeb6` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzReader` (./zipreader) | 2m32s | 1522698 | ok |
| 2026-11-27 11:50 | `ceceeb6` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzRoundTrip` (./zipwriter) | 2m32s | 160123 | ok |
| 2026-11-27 12:54 | `1469779` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzDeflate` (./internal/deflate) | 2m32s | 316434 | ok |
| 2026-11-27 12:56 | `1469779` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzInflate` (./internal/inflate) | 2m32s | 5018023 | ok |
| 2026-11-27 12:59 | `1469779` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzReader` (./zipreader) | 2m33s | 1545926 | ok |
| 2026-11-27 13:01 | `1469779` | lokalnie (darwin/arm64, 10 rdzeni) | `FuzzRoundTrip` (./zipwriter) | 2m32s | 173084 | ok |
| — | `1469779` | Synology StormSyn (DS224+, Celeron J4125, 4 rdzenie) | `FuzzDeflate`, `FuzzInflate`, `FuzzReader`, `FuzzRoundTrip` | zlecone 4 × 375 min (25 h), 3 wątki | — | w toku od 2026-11-27 13:30 (pierwsza w kolejce po powrocie zasilania); koniec około 2026-11-28 14:30; wiersze z wynikiem dopisze `tools/fuzz -import` |
