# Wymagania

Każde wymaganie ma identyfikator `PREFIKS-nnn`. Testy odwołują się do nich przez `REQ: PREFIKS-nnn`; test
`TestRequirementsCovered` sprawdza pokrycie w obie strony.

| Prefiks | Plik | Zakres | Etap | Status |
|---|---|---|---|---|
| `FMT` | `format.md` | inwentarz formatu: rekordy, pola, bity flag, metody, pola dodatkowe; werdykt dla każdego | 0, 1 | przyjęte (wiersze nagłówka lokalnego i deskryptora: etap 2) |
| `DIR` | `directory.md` | koniec katalogu, ZIP64, katalog centralny, lista wpisów | 1 | przyjęte |
| `NAME` | `names.md` | nazwy wpisów: kodowanie, reguły, powtórzenia | 1 | przyjęte; NAME-006 szkic (etap 2) |
| `ENT` | `entries.md` | nagłówki lokalne, deskryptory, układ pliku, treść wpisu, CRC | 2 | szkic |
| `INF` | `inflate.md` | dekompresja DEFLATE | 2 | szkic |
| `SAFE` | `safety.md` | limity, wrogie archiwa, brak wejścia i wyjścia, czas liniowy | 1, 2, 3 | SAFE-001, 002, 007 przyjęte; reszta szkic |
| `DET` | `determinism.md` | powtarzalność, niezależność od systemu i wersji Go, pamięć | 1, 3 | DET-001, 003, 004, 005 przyjęte; reszta szkic |
| `ZW` | `writer.md` | pisarz i kompresja | 4 | szkic |
| `API` | `api.md` | stabilność API, wzorce, kody błędów | 0, 1 | API-001, 003–006 przyjęte; 002 i 007 szkic |
| `EVD` | `evidence.md` | pakiet dowodów (K9) | 5 | szkic |

## Szkic a wymaganie przyjęte

Plik ma wiersz `Status: szkic` albo `Status: przyjęte` (dokładnie te słowa, nic więcej w wierszu); wiersz dotyczy tabel pod nim, do następnego takiego wiersza, więc jeden plik może mieć tabelę przyjętą i tabelę szkiców. Szkice zapisano przy planowaniu (2026-11-26),
żeby sesja produkcyjna znała zamiar; etap zaczyna się od przeglądu swoich szkiców **z tekstem specyfikacji w ręku**
(numery rozdziałów APPNOTE i RFC 1951 przy każdym wymaganiu uzupełnia ten przegląd), poprawienia ich i zmiany
statusu. `TestRequirementsCovered` wymaga testu tylko dla plików przyjętych, ale odwołanie `REQ:` do
nieistniejącego identyfikatora jest błędem zawsze. Identyfikator raz przyjęty nie zmienia znaczenia; wymaganie
wycofane zostaje w pliku z dopiskiem.
