# Surowe wyniki pomiarów

Każdy plik to niezmieniony wynik przebiegu narzędzia z repozytorium, z datą, commitem, wersją Go, systemem
i maszyną w nagłówku. Liczby w `../comparison.md` i w `../testing.md` dają się z tych plików wyprowadzić; sposób
jest w `../methodology.md`. Pliki zastępuje nowy przebieg tym samym narzędziem, nigdy ręczna poprawka.

| Plik | Narzędzie | Co zawiera | Test, który go sprawdza |
|---|---|---|---|
| `compare.tsv` | `go -C tools/compare run . -root ../..` | co każdy czytnik zrobił z każdym archiwum korpusu zgodności i korpusu wrogiego (K3) oraz z archiwami zapisanymi przez `zipwriter` (ZW-007), z rozmiarem i SHA-256 każdego zapisanego archiwum | `TestCompareRecorded` |
| `bench.tsv` | `go -C tools/bench run . -root ../..` | czas i alokacje każdego przebiegu odczytu, gozipgo i `archive/zip` (K7); czas zapisu i rozmiar archiwum dla gozipgo, `archive/zip` i `archive/zip` z `compress/flate` na poziomie domyślnym (ZW-006) | `TestBenchRecorded`, `TestBenchWriteRecorded` |
