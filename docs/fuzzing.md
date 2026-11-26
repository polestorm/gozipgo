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
| `FuzzRoundTrip` | `zipwriter` | dowolne wpisy zapisane pisarzem: czytnik w trybie ścisłym oddaje te same nazwy i treść; drugi zapis daje te same bajty | 4 |
| `FuzzDeflate` | `internal/deflate` | dowolne dane: własna dekompresja i `compress/flate` oddają dane wejściowe | 4 |

Wejścia startowe: korpus zgodności, korpus wrogi i archiwa z budowniczego testów (fuzzer strukturalny: mutuje
pola rekordów, nie tylko bajty).

## Przebiegi

| Data | Commit | Host i sprzęt | Fuzzer | Czas zegarowy | Wykonania | Wynik |
|---|---|---|---|---|---|---|
| — | — | — | — | — | — | kampanii jeszcze nie było; krótkie sprawdzenia lokalne z etapów 1 i 2 są w tabeli pomiarów w `testing.md` |
