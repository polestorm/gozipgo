# 0008 Szybka ścieżka dekodera

Status: przyjęta · 2026-11-27 (decyzja właściciela po prototypie i pomiarze: zmiana wchodzi do v0.2.0)

## Kontekst

Dekoder z ADR 0003 dekodował blok z kodami jedną pętlą: przed każdym symbolem wywołanie dopełniające bufor
bitów, przy każdym symbolu sprawdzenie, czy wystarczy bitów i czy jest miejsce w oknie, a cały stan w polach
struktury. Odczyt spełniał K7 (od 0,76 do 1,32 czasu `archive/zip`), ale `klauspost/compress` czytał znaczniki
o 8%, a wiersze liczb o 32% szybciej. Od ADR 0007 ten sam dekoder pracuje też przy zapisie (samokontrola
pisarza), więc jego czas liczy się dwa razy.

Prototyp (gałąź robocza, 2026-11-27) dał na tych samych danych 721 zamiast 525 MB/s i 417 zamiast 268 MB/s.
Dekoder czyta wrogie dane, więc decyzja zależała od tego, czy da się to zrobić bez drugiego miejsca, w którym
powstają błędy i kończy się strumień.

## Decyzje

1. **Dwie ścieżki przez blok z kodami, jedna dla błędów.** Szybka (`fast`) pracuje tylko wtedy, gdy w buforze
   wejścia jest co najmniej osiem bajtów, a w oknie miejsce na najdłuższe odwołanie (258 bajtów). Wtedy żaden
   krok nie trafi na koniec wejścia ani okna, więc oba sprawdzenia robi raz warunek pętli, a bufor bitów
   i pozycje żyją w zmiennych lokalnych. Zwykła ścieżka z ADR 0003 zostaje bez zmian i obsługuje brzegi:
   ostatnie bajty każdej porcji wejścia, ostatnie 258 bajtów każdego obrotu okna, koniec strumienia.
2. **Szybka ścieżka niczego nie odrzuca.** Bierze tylko znak, odwołanie i koniec bloku. Kod bez symbolu, symbol
   długości 286–287 albo odległości 30–31, odwołanie w bloku bez kodów odległości i odległość sięgająca przed
   początek wyjścia zostawia nietknięte: przywraca bufor bitów sprzed tego symbolu i wraca, a zwykła ścieżka
   dekoduje symbol ponownie i zgłasza błąd. Rodzaj i miejsce błędu mają więc jedno źródło.
3. **Jeden krok zużywa najwyżej 48 bitów** (kod długości 15 i 5 bitów dodatkowych, kod odległości 15 i 13).
   Bufor jest dopełniany ośmioma bajtami naraz, gdy ma mniej niż 48 bitów; po dopełnieniu ma co najmniej 56.
4. **Kody dłuższe niż tablica** (ponad 9 bitów) szybka ścieżka rozstrzyga z tablic kanonicznych, tych samych,
   z których korzysta zwykła, zaczynając od długości 10.
5. **Odwołania są kopiowane dokładnie co do bajta.** Kopiowanie po osiem bajtów z nadmiarem, zwykłe w takich
   pętlach, byłoby tu błędem: okno jest kołowe, więc bajty tuż za bieżącym końcem wyjścia to dane poprzedniego
   obrotu, do których następne odwołanie o odległości bliskiej 32 768 jeszcze sięga. Odwołanie, którego źródło
   zaczyna się w poprzednim obrocie, jest kopiowane w dwóch częściach (do końca okna i od jego początku);
   część zachodząca na cel rośnie przez podwajanie, jak w zwykłej ścieżce.
6. **Przełącznik tylko dla testów.** Pole `slow` struktury `Reader` wyłącza szybką ścieżkę. Jest
   nieeksportowane, pakiet jest wewnętrzny, a kod biblioteki go nie ustawia: służy testom do porównania ścieżek.
7. **Równoważność jest wymaganiem (INF-010)**, nie własnością, którą raz sprawdzono: dla każdego wejścia
   i każdego podziału wejścia i wyjścia obie ścieżki dają te same bajty, ten sam rodzaj i miejsce błędu i tę
   samą liczbę zużytych bajtów. Sprawdzają to `TestFastPathEquivalent` (strumienie `compress/flate` na czterech
   poziomach i ich wersje ucięte, z odwróconym bitem i z podmienionymi bajtami, ziarna fuzzera),
   `TestFastPathWindow` (odwołania na brzegach okna i odległości na granicy początku wyjścia),
   `TestFastPathLongestStep` (kroki po 48 bitów przy każdym stanie bufora, kody każdej długości),
   `TestFastPathRefusals`, `TestFastPathWindowEdges`, `TestRandomStreams` oraz `FuzzInflate`, który porównuje
   ścieżki na każdym wejściu.

## Sprawdzenie testów

Szesnaście usterek wstrzykniętych w szybką ścieżkę (próg dopełnienia bufora, miejsce w oknie, zapas wejścia,
granica odległości, obie części kopii przez koniec okna, kopia bez zachodzenia, kopia z nadmiarem jednego bajta,
licznik zużytych bajtów, brak przywrócenia bufora przed oddaniem symbolu, maska bitów dodatkowych): dwanaście
wykrytych. Cztery pozostałe nie zmieniają wyniku: oba progi wejścia do szybkiej ścieżki przesunięte o jeden
(pętla ma własny warunek), kody długie szukane od długości 11 (symbol trafia wtedy do zwykłej ścieżki)
i pominięte sprawdzenie bloku bez kodów odległości (pusta tablica kodu nie daje symbolu, więc też trafia do
zwykłej). Pierwsza próba zostawiła cztery usterki, które wynik zmieniają, niewykryte: po niej doszły
`TestFastPathLongestStep`, `TestFastPathRefusals`, zapas wejścia w `TestFastPathWindow` i przypadki granicy
odległości.

## Znalezisko fuzzingu w pierwszej wersji

Pierwsza wersja tej zmiany (commit `ca1e2e7`, niewydana) panikowała: `FuzzInflate` znalazł to w pierwszej
sekundzie, `FuzzReader` po trzech minutach. Odwołanie zdekodowane szybką ścieżką może skończyć się dokładnie na
końcu okna; szybka ścieżka wtedy wraca, a zwykła, która zakłada wolne miejsce w oknie, zapisywała następny znak
za oknem (indeks poza tablicą, czyli panika zamiast błędu: naruszenie INF-008, bez zapisu poza pamięć). Prototyp
miał to sprawdzenie; zniknęło przy porządkowaniu kodu, a testy brzegów okna nie miały przypadku, w którym po
takim odwołaniu stoi znak. Poprawka: po powrocie szybkiej ścieżki pełne okno kończy krok. `TestFastPathWindow`
ma teraz ten przypadek (bez poprawki panikuje), a oba wejścia fuzzera są ziarnami. Wniosek zapisany w pkt 7:
testy pisane przez autora zmiany nie zastąpiły tu fuzzera, który porównuje ścieżki na każdym wejściu.

## Granice i niezależny przegląd (2026-11-27)

Na pytanie właściciela, czy szybka ścieżka może zawieść na spreparowanym wejściu, kod został przejrzany dwa
razy: przez autora, linia po linii, i przez osobną sesję, która dostała tylko kod i wymaganie INF-010, bez
rozumowania autora. Oba przeglądy doszły do tych samych granic i żaden nie znalazł błędu.

- **Bity.** Na początku kroku w buforze jest co najmniej 48 ważnych bitów (bez dopełnienia 48–63, po nim
  56–63), a krok zużywa najwyżej 15 + 5 + 15 + 13 = 48. Odejmowanie od licznika bitów nie może więc zejść
  poniżej zera, a `long` zawsze ma swoje 15 bitów.
- **Wejście.** Osiem bajtów jest czytanych tylko pod warunkiem pętli `ip+8 <= ie`; dopełnienie przesuwa `ip`
  o 2–7 bajtów.
- **Tablice.** Indeks tablicy kodu jest maskowany do 9 bitów; tablice długości i odległości są czytane po
  sprawdzeniu symbolu (mniej niż 29 i mniej niż 30); w `long` indeks symbolu jest mniejszy od liczby kodów danej
  długości. Tablice po poprzednim bloku albo strumieniu nie mogą dać symbolu: `build` zeruje liczniki przy
  każdym wywołaniu, a dekoder dostaje kod dopiero po udanym zbudowaniu.
- **Okno.** Przed krokiem `wp <= 32 510`, odwołanie ma najwyżej 258 bajtów, więc kopia kończy się najdalej na
  końcu okna. Źródło w bieżącym obrocie leży przed celem i jest z nim rozłączne. Źródło w poprzednim obrocie
  wymaga, żeby okno było już raz zapełnione przez ten strumień, i leży w oknie nie wcześniej niż cel, więc
  kopia w przód czyta każdy bajt, zanim go nadpisze; przy odległości 32 768 źródło i cel to to samo miejsce.
  Gdy źródło dochodzi do końca okna, cel stoi dokładnie na pozycji równej odległości, więc dalszy ciąg to
  zwykłe odwołanie o tej odległości od początku okna.
- **Koniec pracy.** Każdy obrót pętli w `symbols` posuwa dekodowanie, kończy się błędem albo wraca; kopia
  zawsze przenosi co najmniej jeden bajt.
- **Błędy.** Każda odmowa szybkiej ścieżki przywraca bufor bitów sprzed symbolu, więc zwykła ścieżka dekoduje
  te same bity: rodzaj i miejsce błędu powstają w jednym miejscu kodu.

Przegląd wskazał jedną kruchość poza szybką ścieżką, dziś nieosiągalną: gałąź ośmiobajtowa `fill` liczyła
`(63 − nb) >> 3` na liczbach bez znaku, a pętla po bajcie potrafi zostawić w buforze 64 bity. Stan był
bezpieczny tylko dlatego, że po każdym dopełnieniu zużywany jest co najmniej jeden bit. `fill` wraca teraz od
razu, gdy bufor ma więcej niż 56 bitów (`TestFillFullBuffer`); szybkość bez zmian. Jedna gałąź szybkiej ścieżki
jest nieosiągalna z założenia (przywrócenie bufora, gdy kod znaków nie daje symbolu: kod znaków jest zawsze
zupełny) i zostaje jako obrona.

Z przeglądu pochodzi też `TestRandomStreams`: generator losowych strumieni o wielu blokach z losowymi zupełnymi
kodami do 15 bitów, sprawdzany z własnym modelem wyniku, z `compress/flate`, między ścieżkami, ze źródłem
o losowych odczytach i na jednym dekoderze używanym przez `Reset`. Pełny przebieg przeglądu: 42 000 strumieni
(26 680 poprawnych, 15 320 uszkodzonych), bez różnicy. `TestFastPathWindowEdges` przechodzi systematycznie
przez okolice końca okna (ponad 5000 układów miejsca, długości, odległości i następnego symbolu).

Czego to nie dowodzi: granice są rozumowaniem dwóch czytających, nie dowodem sprawdzonym maszynowo, a testy
i fuzzing są próbkowaniem. Go sprawdza indeksy, więc błąd zakresu byłby paniką (odmową usługi), nie zapisem
poza pamięć; błąd, który dawałby złe bajty bez paniki, musiałby leżeć w rachunku kopii opisanym wyżej.

## Pomiar

`tools/bench` na commicie `2aa8721`, Apple M4, mediana z 7 przebiegów; surowy wynik w `docs/results/`, tabele
w `docs/comparison.md`. Przed zmianą (`1d5412e`) / po / `klauspost/compress` / `archive/zip`:

| Przypadek | Przed | Po | `klauspost/compress` | `archive/zip` |
|---|---|---|---|---|
| odczyt, znaczniki | 525 MB/s | 720 MB/s | 562 MB/s | 395 MB/s |
| odczyt, wiersze liczb | 268 MB/s | 423 MB/s | 354 MB/s | 237 MB/s |
| odczyt, zera | 3926 MB/s | 4534 MB/s | 5269 MB/s | 4940 MB/s |
| odczyt 100 000 małych wpisów z otwarciem | 753 ms | 559 ms | 524 ms | 784 ms |
| zapis z samokontrolą, znaczniki | 180 MB/s | 196 MB/s | 337 MB/s | 49 MB/s |
| zapis z samokontrolą, wiersze liczb | 85 MB/s | 97 MB/s | 139 MB/s | 46 MB/s |

K7: czas odczytu treści względem `archive/zip` od 0,55 do 1,09 (było od 0,76 do 1,32; próg 1,5). Samokontrola
pisarza kosztuje teraz około jednej czwartej szybkości zapisu (bez niej 268 i 127 MB/s).

## Skutki

- Bajty wyjścia dekodera, kody błędów i ich miejsca nie zmieniają się; publiczne API bez zmian; wzorce bajtów
  pisarza bez zmian.
- Samokontrola pisarza (ADR 0007 pkt 7) tanieje razem z dekoderem.
- Kampania fuzzingu dla v0.2.0 biegnie na commicie z tą zmianą; `FuzzInflate` niesie nowy niezmiennik.
- K4 obejmuje 87 wymagań.
