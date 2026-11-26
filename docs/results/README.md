# Surowe wyniki pomiarów

Każdy plik to niezmieniony wynik przebiegu narzędzia z repozytorium, z datą, commitem, wersją Go, systemem
i maszyną w nagłówku. Liczby w `../comparison.md` i w `../testing.md` dają się z tych plików wyprowadzić; sposób
jest w `../methodology.md`. Pliki zastępuje nowy przebieg tym samym narzędziem, nigdy ręczna poprawka.

| Plik | Narzędzie | Co zawiera | Test, który go sprawdza |
|---|---|---|---|
| `compare.tsv` | `go -C tools/compare run . -root ../..` | co każdy czytnik zrobił z każdym archiwum korpusu zgodności i korpusu wrogiego (K3) | `TestCompareRecorded` |
| `bench.tsv` | `go -C tools/bench run . -root ../..` | czas i alokacje każdego przebiegu, gozipgo i `archive/zip` (K7) | `TestBenchRecorded` |
