# Wymagania: pakiet dowodów

Status: przyjęte

Kryterium K9 (ADR 0001). Przyjęte w etapie 5 (2026-11-27); narzędzie `tools/evidence` przeniesione z rodziny
i dostosowane, testy w `tools/evidence/evidence_test.go`.

| Id | Wymaganie |
|---|---|
| EVD-001 | Pakiet zawiera surowy wynik `go test -count=1 -json ./...` modułu głównego i modułów narzędzi, z commitem (i oznaczeniem zmian w drzewie roboczym), wersją Go, systemem i datą. |
| EVD-002 | Pakiet zawiera macierz śledzenia: każde przyjęte wymaganie z testami, które się na nie powołują przez `REQ:`, i z ich wynikiem; wymaganie bez testu albo z testem pominiętym jest wykazane jako niespełnione. Zawiera też inwentarz formatu z werdyktami (K1). |
| EVD-003 | Pakiet zawiera tabelę K1–K9: wartość, cel, stan i źródło. Wartości z przebiegów testów liczy z ich wyjścia; pomiary spoza `go test` (K3, K5, K7, K8 u konsumenta) bierze z plików w `docs/` z datą i commitem pomiaru. |
| EVD-004 | Dla tego samego commitu i tych samych danych raport i jego postać maszynowa różnią się tylko datą wygenerowania; czasy przebiegów są wyłącznie w plikach surowych. |
| EVD-005 | Narzędzie nie sięga do sieci, nie uruchamia fuzzingu ani programów zewnętrznych i nie czyta danych spoza repozytorium. Kończy się kodem 1, gdy K4 albo K9 nie są spełnione; pakiet zapisuje także wtedy. |
