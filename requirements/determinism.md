# Wymagania: powtarzalność i zasoby

Status: przyjęte

Przyjęte w etapie 1 w zakresie listy wpisów i błędów `New`, w etapie 2 (2026-11-27) w zakresie treści wpisów; DET-002 i DET-007 w etapie 3 (2026-11-27, ADR 0004).

| Id | Wymaganie |
|---|---|
| DET-001 | To samo archiwum i te same opcje dają tę samą listę wpisów, te same bajty treści i ten sam błąd (kod, wpis, przesunięcie) w każdym przebiegu; 10 przebiegów na korpusie daje identyczne zrzuty. |
| DET-003 | Wynik nie zależy od kolejności iteracji po mapach, od czasu, strefy czasowej, zmiennych środowiskowych ani ustawień regionalnych; data wpisu jest wydawana tak, jak zapisano (DIR-010). |
| DET-004 | Wynik nie zależy od tego, jak `io.ReaderAt` dzieli odczyty ani od rozmiaru buforów: czytnik karmiony źródłem zwracającym po jednym bajcie daje to samo. |
| DET-005 | Gdy archiwum ma więcej niż jedną wadę, zgłaszana jest ta, którą wyznacza ustalona kolejność kroków zapisana w `docs/errors.md` i w dokumentacji `New`; kolejność jest częścią kontraktu i ma test. |
| DET-006 | `go test -race` przechodzi; równoległe `Open` na jednym czytniku daje te same wyniki co kolejne. |
| DET-002 | Wynik nie zależy od systemu ani architektury z macierzy CI (Linux, macOS, Windows; bieżące wydanie Go): wzorce korpusów (manifest korpusu zgodności, `testdata/hostile.json`, werdykty zestawów pobranych) są wspólne dla wszystkich. |
| DET-007 | Szybkość i pamięć są mierzone narzędziem z repozytorium i zapisane z datą (K7); próg wydajności dekompresji względem `archive/zip` jest sprawdzany w tym samym przebiegu na tych samych danych. |
