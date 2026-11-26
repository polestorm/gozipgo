# Wymagania: API i zgodność

Status: przyjęte

| Id | Wymaganie |
|---|---|
| API-001 | Publiczne API każdego pakietu jest zapisane we wzorcu `api/<pakiet>.txt`; `TestAPISnapshot` porównuje wzorzec z kodem. Zmiana wzorca wymaga `-update` i wpisu w ADR albo w `CHANGELOG.md`. |
| API-003 | Kody błędów są stałymi eksportowanymi, opisanymi w `docs/errors.md`; test sprawdza, że każdy kod z kodu źródłowego jest w katalogu i odwrotnie, i że każdy kod ma test, który go wywołuje. Kod raz wydany nie zmienia znaczenia i nie znika. |
| API-004 | Komunikaty błędów są po angielsku i nie są częścią kontraktu; kontraktem jest kod, indeks i nazwa wpisu oraz przesunięcie. |
| API-005 | Każdy eksportowany identyfikator ma komentarz dokumentacyjny po angielsku; każdy pakiet ma `Example*` dla głównej drogi użycia, sprawdzany przez `go test`. |
| API-006 | Moduł główny nie ma sekcji `require` i buduje się z `CGO_ENABLED=0`; najniższa wersja Go z `go.mod` jest w macierzy CI. |

Status: szkic

Reguły procesu wydań; obowiązują od v0.1.0 i dostają test razem z pierwszym wydaniem (wzorzec API poprzedniej wersji, zapadka werdyktów).

| Id | Wymaganie |
|---|---|
| API-002 | W wersjach 0.x zmiana niezgodna wymaga ADR; od 1.0.0 obowiązuje zgodność wsteczna w obrębie wersji głównej. |
| API-007 | Zbiór archiwów przyjmowanych w trybie ścisłym może się między wersjami tylko zawężać z wpisem w `CHANGELOG.md` jako poprawka bezpieczeństwa albo rozszerzać przez nową opcję; domyślne zachowanie wobec danego archiwum nie zmienia się bez wpisu. |
