# Wymagania: powtarzalność i zasoby

Status: przyjęte

Przyjęte w etapie 1 w zakresie listy wpisów i błędów `New`; treść wpisów obejmą w etapie 2 tymi samymi testami.

| Id | Wymaganie |
|---|---|
| DET-001 | To samo archiwum i te same opcje dają tę samą listę wpisów, te same bajty treści i ten sam błąd (kod, wpis, przesunięcie) w każdym przebiegu; 10 przebiegów na korpusie daje identyczne zrzuty. |
| DET-003 | Wynik nie zależy od kolejności iteracji po mapach, od czasu, strefy czasowej, zmiennych środowiskowych ani ustawień regionalnych; data wpisu jest wydawana tak, jak zapisano (DIR-010). |
| DET-004 | Wynik nie zależy od tego, jak `io.ReaderAt` dzieli odczyty ani od rozmiaru buforów: czytnik karmiony źródłem zwracającym po jednym bajcie daje to samo. |
| DET-005 | Gdy archiwum ma więcej niż jedną wadę, zgłaszana jest ta, którą wyznacza ustalona kolejność kroków zapisana w `docs/errors.md` i w dokumentacji `New`; kolejność jest częścią kontraktu i ma test. |

Status: szkic

Macierz CI, odczyt równoległy i pomiary (etapy 2 i 3).

| Id | Wymaganie |
|---|---|
| DET-002 | Wynik nie zależy od systemu, architektury ani wersji Go z macierzy CI: wzorce korpusu są wspólne. |
| DET-006 | `go test -race` przechodzi; równoległe `Open` na jednym czytniku daje te same wyniki co kolejne. |
| DET-007 | Szybkość i pamięć są mierzone narzędziem z repozytorium i zapisane z datą (K7); próg wydajności dekompresji względem `archive/zip` jest sprawdzany w tym samym przebiegu na tych samych danych. |
