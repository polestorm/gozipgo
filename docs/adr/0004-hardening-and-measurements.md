# 0004 Utwardzanie i pomiary (etap 3)

Status: przyjęta (robocza, do potwierdzenia przez właściciela) · 2026-11-27

## Kontekst

Etap 3 mierzy to, co etapy 1 i 2 zbudowały: korpus wrogi (K6), zachowanie innych programów na tych samych
archiwach (K3), szybkość i pamięć (K7), kampanię fuzzingu (K5). Pomiary pokazały trzy miejsca do poprawy w kodzie
i wymusiły kilka decyzji o tym, co i jak liczymy.

## Decyzje

1. **Korpus wrogi jest budowany, nie przechowywany.** 39 archiwów w 14 rodzinach SAFE-003 buduje test
   (`hostileArchives`), bajt w bajt tak samo na każdym systemie i w każdej wersji Go: strumienie DEFLATE są
   zapisane dosłownie albo złożone bit po bicie z RFC 1951, żaden nie pochodzi z `compress/flate`. Repozytorium
   trzyma manifest `testdata/hostile.json` (opis, oczekiwany kod, SHA-256); pliki zapisuje
   `go test ./zipreader -run TestHostileArchives -write-hostile KATALOG`. Bomba ma stosunek 1032:1, największy
   możliwy w DEFLATE (jeden blok dynamiczny, odwołanie w dwóch bitach).
2. **K3 mierzymy na sześciu czytnikach, a nie na czterech:** do `archive/zip`, Info-ZIP, 7-Zip i `zipfile` Pythona
   doszedł bsdtar (libarchive), bo jest na każdym macOS i Linuksie. Zgodność na korpusie zgodności liczymy po
   treści (SHA-256 każdego pliku); nazwy porównujemy tylko dla gozipgo, bo programy rozpakowujące na dysk
   zmieniają je zależnie od systemu plików i ustawień regionalnych (`methodology.md`).
3. **Wynik K3 z 2026-11-27:** gozipgo 22/22 na korpusie zgodności i 39/39 odrzuconych na korpusie wrogim.
   `archive/zip`, 7-Zip i Python 22/22; unzip w wydaniu Apple 18/22 i bsdtar 21/22, z przyczyn leżących po ich
   stronie (`differences.md`). Kryterium „ta sama lista i treść co każda czarna skrzynka” jest więc spełnione
   wobec trzech z pięciu; dla dwóch pozostałych różnica jest opisana i nie dotyczy naszego odczytu.
4. **Tabela wrogich archiwów nie jest rankingiem.** Większość tych archiwów nie łamie litery specyfikacji,
   a programy rozpakowujące mają własne zabezpieczenia. Tabela pokazuje, gdzie jeden plik znaczy co innego dla
   różnych czytników (np. `two-ends-comment`: 7-Zip czyta inne pliki niż pozostali).
5. **Poprawki po pomiarze szybkości.** (a) Kopiowanie odwołania zachodzącego na własny cel (odległość mniejsza
   niż długość) szło bajt po bajcie; plik z samych zer był 9× wolniejszy niż w `archive/zip`. Teraz kopiujemy
   kawałkami, z których każdy sięga do bieżącego końca wyjścia: 1,3×. (b) Dekoder DEFLATE (około 46 KiB) był
   alokowany przy każdym `Open`; przy 100 000 małych wpisów dawało to 4,7 GiB śmieci. `Close` czytnika wpisu
   oddaje teraz dekoder do puli (`sync.Pool`), a `inflate.Reader.Reset` przygotowuje go do nowego strumienia;
   okno poprzedniego strumienia nie może trafić do wyjścia, bo odległość jest sprawdzana względem bajtów
   wydanych w bieżącym strumieniu. (c) Lista wpisów ma od razu pojemność równą liczbie wpisów, która jest już
   ograniczona limitem i rozmiarem katalogu w pliku; porównanie nazw przy wykrywaniu powtórzeń pomija wspólny
   początek bez liczenia rang.
6. **Nowy import w kodzie biblioteki: `sync`** (pula dekoderów). Bez wejścia i wyjścia; lista w `api_test.go`.
7. **Otwarcie archiwum jest wolniejsze niż w `archive/zip` i tak zostaje.** `New` czyta każdy nagłówek lokalny
   (ADR 0003 pkt 1); przy 100 000 wpisów to około 0,45 µs na wpis wobec 0,09 µs. K7 mówi o rozpakowaniu; koszt
   otwarcia podajemy w tabeli osobno.
8. **Wyniki pomiarów mają testy.** `TestCompareRecorded` i `TestBenchRecorded` czytają surowe pliki
   z `docs/results/` i sprawdzają na nich K3 (dla gozipgo) i próg K7. Test nie mierzy sam, więc nie zależy od
   obciążenia maszyny; nieaktualny pomiar (inna liczba archiwów niż w manifestach) jest błędem.
9. **Domyślny limit stosunku zostaje 1000:1** (ADR 0003 pkt 8). Skutek dla użytkownika: wpis ponad 1 MiB, który
   kompresuje się lepiej niż 1000:1, `Open` odrzuca kodem `limit-ratio`. W praktyce to pliki złożone z jednego
   powtarzanego bajtu (puste obrazy dysków, wyzerowane bufory); zwykłe dokumenty, arkusze i XML mają stosunek
   rzędu 3–20:1, a skrajnie powtarzalne dane około 100–300:1. Kto takie pliki przyjmuje, ustawia `MaxRatio`
   wyżej albo −1; limity rozmiaru wpisu i rozmiaru łącznego działają nadal.

## Skutki

- K1, K2, K3 (z opisanymi wyjątkami po stronie dwóch programów), K4, K6 i K7 są zmierzone. K5 czeka na kampanię
  24 h na Synology (zlecona 2026-11-27 na commicie `4a0326b`, w kolejce za trzema zadaniami innych projektów);
  K8 w części konsumenta i K9 należą do etapu 5.
- Kod biblioteki zmienił się po zleceniu kampanii (pkt 5), więc do K5 dla wydania v0.2.0 potrzebna będzie
  kampania na commicie z tymi zmianami; przebieg na `4a0326b` liczy się dla v0.1.0.
