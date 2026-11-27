# 0003 Nagłówki lokalne, układ pliku i treść wpisów (etap 2)

Status: przyjęta · 2026-11-27 (potwierdzona przez właściciela 2026-11-27)

## Kontekst

Etap 2 dodaje to, bez czego czytnik nie nadaje się do wrogiego wejścia: porównanie nagłówków lokalnych
z katalogiem, sprawdzenie układu pliku i odczyt treści. Właściciel rozstrzygnął 2026-11-26 pytanie 1: dekompresja
DEFLATE jest własna. Szkice wymagań `ENT` i `INF` przejrzano z APPNOTE 6.3.10 i RFC 1951 w ręku i na archiwach:
22 z korpusu zgodności i 568 z zestawów pobranych. Tam, gdzie prawdziwe programy pokazały co innego niż szkic,
decyduje to, co piszą programy, o ile nie otwiera to drugiego odczytania archiwum.

## Decyzje

1. **`New` czyta wszystkie nagłówki lokalne i deskryptory.** Po `New` wiadomo, że katalog, nagłówki lokalne
   i deskryptory opisują jeden układ pliku; `Open` sprawdza już tylko treść. Koszt to jeden mały odczyt na wpis
   (przez bufor, w kolejności położenia). Błąd układu nie może się więc ujawnić dopiero przy którymś z kolei
   `Open`, po tym, jak wywołujący przetworzył część wpisów.
2. **Obszar wpisu kończy się tam, gdzie zaczyna się następny** (w kolejności położenia) albo katalog. Stąd trzy
   sprawdzenia bez zgadywania: nagłówek z danymi dłuższy niż obszar to nachodzenie (`zip-entries-overlap`); bajty
   po danych wpisu bez bitu 3 to dane, na które nic nie wskazuje (`zip-unreferenced-data`); bajty po danych wpisu
   z bitem 3 muszą być deskryptorem.
3. **Postać deskryptora wynika z długości.** APPNOTE 4.3.9 dopuszcza deskryptor z sygnaturą i bez niej, z polami
   4- i 8-bajtowymi, a programy czytające strumieniem zgadują postać po wartościach. Tu długość obszaru jest
   znana: 12, 16, 20 albo 24 bajty wyznaczają postać, a wartości muszą się równać katalogowi. Deskryptor
   8-bajtowy przy wpisie bez ZIP64 i 4-bajtowy przy wpisie ZIP64 są przyjmowane, gdy wartości się zgadzają: nie ma
   tu drugiego odczytania.
4. **Co w nagłówku lokalnym musi się zgadzać z katalogiem:** całe pole flag, metoda, nazwa bajt w bajt, CRC-32
   i rozmiary (z bitem 3: zero albo wartość z katalogu). **Czego nie porównujemy:** wersji wymaganej (Info-ZIP
   z `-fz` zapisuje w nagłówku lokalnym 4.5, a w katalogu 1.0 albo 2.0) oraz daty i czasu. Nie wpływają na to,
   jak czyta się treść; o zakresie (`unsupported-version`) decyduje wartość z katalogu.
5. **Pola dodatkowe nagłówka lokalnego** mogą się różnić od pól katalogu (znaczniki czasu, identyfikatory), ale
   muszą mieć poprawną strukturę. Trzy są interpretowane. ZIP64: wymagane przy wartości granicznej rozmiaru,
   zawsze z oboma rozmiarami, a pole rozmiaru bez wartości granicznej musi mu być równe (`zipfile` Pythona
   zapisuje pole ZIP64 obok prawdziwych rozmiarów). Unicode Path: musi dawać nazwę wpisu po zdekodowaniu (7-Zip
   na Windows zapisuje je tylko w nagłówku lokalnym; to niezależne potwierdzenie wybranej strony kodowej, a przy
   złej stronie kodowej sprzeczność). Pola szyfrowania i łatki nie mogą wystąpić przy wpisie, którego katalog tak
   nie oznacza.
6. **Dane wstępne (pytanie 3): opcja `AllowPrefix`, domyślnie wyłączona.** Dopuszcza bajty przed pierwszym
   wpisem w obu sposobach liczenia przesunięć: od początku pliku (archiwum poprawione po doklejeniu) i od końca
   danych wstępnych (archiwum doklejone do programu). Który obowiązuje, wynika jednoznacznie z tego, że katalog
   musi kończyć się tam, gdzie zaczynają się rekordy końca, a rekord ZIP64 tam, gdzie zaczyna się lokalizator.
   `Entry.LocalHeaderOffset` podaje położenie w pliku. Użycie opcji widać w `Relaxed()` z wpisem −1.
7. **Opcji `AllowGaps` nie ma.** Żadne archiwum z prawdziwego programu w korpusie zgodności nie ma luk między
   wpisami; mają je tylko pliki testowe libzip (`gap*.zip`), które pokazują właśnie archiwa po usuniętych wpisach.
   Luka to miejsce na treść, której lista wpisów nie pokazuje, więc opcja powstanie dopiero wtedy, gdy konsument
   pokaże archiwum, którego nie da się inaczej przeczytać.
8. **Limity treści (pytanie 9):** wpis 1 GiB, łącznie 4 GiB, stosunek 1000:1 dla treści ponad 1 MiB. Rozmiar
   wpisu i stosunek sprawdza `Open` na polach katalogu; to bezpieczne, bo odczyt nigdy nie wydaje więcej bajtów,
   niż katalog deklaruje, więc kłamiący nagłówek kończy się błędem `zip-size-mismatch`, a nie większą treścią.
   Limit łączny jest liczony na bajtach wydanych, atomowo; bajty do limitu są wydawane, więc wynik nie zależy od
   rozmiaru buforów. Uwaga do stosunku: DEFLATE osiąga około 1030:1 na ciągu jednakowych bajtów, więc plik
   ponad 1 MiB złożony z samych zer jest domyślnie odrzucany; kto takie przyjmuje, podnosi `MaxRatio`.
9. **`Open(e Entry)` przyjmuje wpis z listy czytnika.** Liczy się indeks; nazwa i położenie muszą się zgadzać
   z wpisem czytnika, inaczej `ErrNoEntry`. Wywołujący nie może więc podać własnych rozmiarów ani położenia.
   Opcji odroczonego sprawdzania nagłówków (`LazyLocalHeaders` ze szkicu) nie ma: pkt 1. `Open` jest bezpieczne
   przy użyciu równoległym (pytanie 12); `Close` czytnika wpisu nie zamyka archiwum.
10. **Dekoder DEFLATE: reguły, których RFC 1951 nie rozstrzyga wprost.**
    - Kod długości kodów i kod znaków muszą być zupełne. RFC opisuje kody Huffmana, a jedyny wyjątek, który
      wymienia, dotyczy kodu odległości (3.2.7): jeden kod o długości 1 albo żadnego.
    - HLIT najwyżej 286 (3.2.7 podaje zakres 257–286) i HDIST najwyżej 30: pole HDIST mieści 31 i 32, ale
      symbole odległości 30 i 31 nie występują w danych (3.2.6), a `compress/flate` taki opis odrzuca; przyjmując
      go, mielibyśmy strumień o dwóch werdyktach.
    - Bity do granicy bajtu przed blokiem niezakodowanym są pomijane bez sprawdzania wartości (3.2.4).
    - Koniec strumienia musi wypaść na końcu danych skompresowanych wpisu; bity dopełniające ostatni bajt
      należą do strumienia.
    - Bajty zdekodowane przed wadą są wydawane przed błędem.
    Test różnicowy i `FuzzInflate` wymagają, żeby strumień przyjęty przez nasz dekoder przyjmował też
    `compress/flate` z tym samym wynikiem; odwrotnie nie, i każdą taką różnicę zapisujemy w `differences.md`.
    Jedna znana różnica: blok dynamiczny, którego kod znaków ma tylko kod końca bloku (jeden kod o długości 1),
    `compress/flate` przyjmuje jako pusty, a my odrzucamy (`inflate-code-lengths`).
11. **Kolejność błędów** jest stała i zapisana w `docs/errors.md`: po katalogu przesunięcia nagłówków w kolejności
    katalogu, potem wpisy w kolejności położenia. Odczyt treści zgłasza pierwszą wadę strumienia, a na jego
    końcu kolejno: dane po strumieniu, treść krótszą niż zadeklarowana, CRC-32.
12. **Nowe importy biblioteki standardowej w kodzie biblioteki:** `math/bits` (odwracanie bitów kodu) i
    `sync/atomic` (licznik limitu łącznego). Żaden nie ma wejścia ani wyjścia; lista w `api_test.go`.

## Skutki

- Archiwa, które `archive/zip` otwiera, a my odrzucamy, przybyły: wszystkie z rodziny „katalog i nagłówek lokalny
  mówią co innego”, z lukami i z nachodzeniem (`differences.md`). To cel projektu, nie koszt uboczny.
- `New` czyta cały plik „po wierzchu” (nagłówek na wpis), więc dla archiwum na wolnym nośniku jest wolniejsze
  niż samo czytanie katalogu. Pomiar w etapie 3 (K7).
- Szybkość dekodera we wstępnym pomiarze (`BenchmarkContent`, 8 MiB tekstu, Apple M-series, go1.26.6): 187 MB/s
  wobec 173 MB/s `archive/zip`. To nie jest pomiar K7; ten robi `tools/bench` według `methodology.md`.
