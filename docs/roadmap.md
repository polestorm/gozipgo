# Plan

Szacunki z 2026-11-26, w godzinach pracy z asystentem. Kryteria zakończenia całości: ADR 0001, kryteria K1–K9.

| Etap | Zakres | Szacunek | Wydanie | Stan |
|---|---|---|---|---|
| 0 | infrastruktura: `tools/fetch`, `tools/fuzz`, `tools/evidence` (przeniesione z rodziny), budowniczy archiwów testowych (`internal/testkit`: składa archiwum rekord po rekordzie, także niepoprawne), korpus zgodności z manifestem i skryptem odtworzenia, inwentarz formatu z APPNOTE (`requirements/format.md`), `TestRequirementsCovered`, `TestAPISnapshot`, CI | 4–6 h | — | wykonany 2026-11-26; korpus bez Javy, Worda i LibreOffice (uzupełnia właściciel); zestawów pobieranych nie wskazano |
| 1 | kontener: koniec katalogu, ZIP64, katalog centralny, nazwy, limity przed alokacją, lista wpisów, typ `Error` i `docs/errors.md`, `TestFormatInventory` (K1) dla katalogu | 5–8 h | — | wykonany 2026-11-26 (ADR 0002); do decyzji właściciela: domyślne `UnflaggedNames` |
| 2 | treść: nagłówki lokalne i ich zgodność z katalogiem, deskryptory danych, rozłączność i luki, Stored, dekompresja DEFLATE (pytanie 1), CRC-32, limity rozmiaru i stosunku, `Open` i `OpenRaw`; `TestInteropCorpus` (K2) | 8–12 h | **v0.1.0** | wykonany 2026-11-27 (ADR 0003): K1 218/218, K2 22/22 z treścią; v0.1.0 wydane 2026-11-27 (`4a0326b`, tag podpisany) |
| 3 | utwardzanie i porównanie: korpus wrogi (K6), `tools/compare` z czarnymi skrzynkami i `comparison.md` (K3), przypadki patologiczne, archiwum > 4 GB z wejścia syntetycznego, `tools/bench` i pomiary (K7), kampania fuzzingu 24 h na Synology (K5), `methodology.md` | 6–10 h | v0.2.0 | wykonany 2026-11-27 (ADR 0004) poza kampanią fuzzingu: K3, K6, K7 zmierzone; K5 czeka na kampanię na Synology; wydanie razem z etapem 4 |
| 4 | pisarz: kontener, Stored, kompresor DEFLATE o ustalonym algorytmie (pytanie 2), ZIP64, zapis powrotny czytnik ↔ pisarz, przyjęcie przez czarne skrzynki, te same bajty w dwóch wersjach Go (K8) | 10–15 h | v0.2.0 (razem z etapem 3) | wykonany 2026-11-27 (ADR 0005): `zipwriter`, własny kompresor (`internal/deflate`), ZIP64, zapis powrotny, czarne skrzynki, wzorce bajtów w dwóch wersjach Go; **v0.2.0 obejmuje etapy 3 i 4** (decyzja właściciela z 2026-11-27) i wychodzi po kampanii fuzzingu na commicie kandydata; numeru v0.3.0 nie będzie |
| 5 | odbiór: pomiar K1–K9, pakiet dowodów, próba u pierwszego konsumenta (`godocxgo` na swoim korpusie) | 3–5 h | v1.0.0 po K1–K9 | — |
| | **razem** | **36–56 h** | | |

Rozmiar docelowy: 2,5–3,5 tys. linii kodu bez testów (kontener ok. 1,0 tys., dekompresja ok. 0,5 tys., pisarz
ok. 0,6 tys., kompresor ok. 0,8 tys.).

## Kolejność i bramki

- przed etapem 2 decyzja w pytaniu 1 (własna dekompresja; rekomendacja: tak);
- **po etapie 2 (v0.1.0)** `godocxgo` może zacząć swój etap 1 (potrzebuje też `goxmlgo` v0.2.0). Etap 3 biegnie
  równolegle z pracą konsumenta, a jego znaleziska wracają tu jako wymagania;
- etap 4 (pisarz) nie blokuje żadnego obecnego konsumenta: powstaje, gdy pojawi się piszący (zapis DOCX albo XLSX,
  paczki JPK). Można go odłożyć za etap 5 dla samego czytnika; wtedy v1.0.0 obejmuje czytnik, a pisarz wchodzi
  w 1.1.0 (decyzja właściciela przy etapie 3);
- repozytorium publiczne od v0.1.0; założenie i pierwsze wypchnięcie na prośbę właściciela.

## Wydania

Tagi podpisane (`git tag -s vX.Y.Z -m vX.Y.Z`, sprawdzenie `git tag -v` przed wypchnięciem), `CHANGELOG.md` od
v0.1.0 z informacją, że wszystkie tagi są podpisane. Wersje 0.x: API może rosnąć, zmiany niezgodne tylko z wpisem
w ADR i w `CHANGELOG.md`.
