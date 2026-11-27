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
   `TestFastPathRefusals` oraz `FuzzInflate`, który porównuje ścieżki na każdym wejściu.

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

## Pomiar

`tools/bench`, Apple M4, mediana z 7 przebiegów; liczby i surowy wynik w `docs/comparison.md` i `docs/results/`.
Uzupełnione po pomiarze na commicie z tą zmianą: patrz wiersz w `docs/testing.md`.

## Skutki

- Bajty wyjścia dekodera, kody błędów i ich miejsca nie zmieniają się; publiczne API bez zmian; wzorce bajtów
  pisarza bez zmian.
- Samokontrola pisarza (ADR 0007 pkt 7) tanieje razem z dekoderem.
- Kampania fuzzingu dla v0.2.0 biegnie na commicie z tą zmianą; `FuzzInflate` niesie nowy niezmiennik.
- K4 obejmuje 87 wymagań.
