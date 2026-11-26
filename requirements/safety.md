# Wymagania: bezpieczeństwo

Status: przyjęte

Przyjęte w etapie 1 w zakresie `New` (katalog), w etapie 2 (2026-11-27) w zakresie nagłówków lokalnych i odczytu treści.

| Id | Wymaganie |
|---|---|
| SAFE-001 | Pakiety biblioteki nie importują `os`, `net`, `net/http`, `os/exec`, `syscall`, `unsafe`, `plugin` ani `archive/zip` (ani `compress/flate`, jeśli dekompresja jest własna); test sprawdza importy każdego pakietu biblioteki (bez plików testów) względem listy dozwolonych; `fmt` nie jest na liście, bo wciąga `os`. Jedyne wejście to `io.ReaderAt` podany przez wywołującego. |
| SAFE-002 | Ustawienia domyślne są bezpieczne: zerowe `Options` włączają tryb ścisły i wszystkie limity o wartościach domyślnych z dokumentacji (pytanie 9); wyłączenie limitu wymaga jawnej wartości ujemnej, poluzowanie ścisłości jawnej opcji. |
| SAFE-007 | Żadna funkcja publiczna nie panikuje dla żadnego wejścia, także gdy `io.ReaderAt` zwraca błędy, krótkie odczyty albo dane zmieniające się między odczytami; błąd `io.ReaderAt` jest zwracany bez zmiany (źródło krótsze niż podany rozmiar: `io.ErrUnexpectedEOF`). |
| SAFE-004 | Limity treści działają na bajtach rzeczywiście wydanych: `Open` odmawia wpisu, którego zadeklarowany rozmiar przekracza `MaxEntrySize` albo którego stosunek rozmiaru do danych skompresowanych przekracza `MaxRatio` (dla treści ponad 1 MiB), a odczyt nigdy nie wydaje więcej bajtów, niż zadeklarowano (ENT-008), więc pola nagłówków nie pozwalają limitu ominąć; rozmiar łączny (`MaxTotalSize`) jest liczony na bajtach wydanych przez wszystkie odczyty czytnika i przerywa odczyt w chwili przekroczenia. |

Status: szkic

Etap 3 (korpus wrogi, pomiary czasu i pamięci).

| Id | Wymaganie |
|---|---|
| SAFE-003 | Wrogie archiwa są odrzucane błędem ze stałym kodem, każde w < 1 s i < 64 MB: (a) bomba klasyczna: wpis o stosunku kompresji ponad limit; (b) bomba z kłamiącym nagłówkiem: zadeklarowany rozmiar mały, strumień długi; (c) wpisy nakładające się na te same dane skompresowane (wiele wpisów katalogu, jeden strumień); (d) nagłówki lokalne „cytujące” następne wpisy w bloku niezakodowanym; (e) liczba wpisów ponad limit i liczba wpisów sprzeczna z rozmiarem katalogu; (f) katalog poza plikiem, katalog nachodzący na dane; (g) dwa końce katalogu, archiwum doklejone do archiwum, dane po końcu katalogu; (h) katalog i nagłówek lokalny z inną nazwą, metodą albo rozmiarem; (i) dane, na które nie wskazuje katalog; (j) nazwy `../`, bezwzględne, z odwrotnym ukośnikiem, z bajtem zerowym, powtórzone; (k) ZIP64 z rozmiarami i przesunięciami poza plikiem albo sprzecznymi z rekordem zwykłym; (l) pola dodatkowe o długości wychodzącej poza nagłówek; (m) plik obcięty w każdym miejscu struktury; (n) strumień DEFLATE niepoprawny w każdej z kategorii INF. Archiwa buduje test. |
| SAFE-005 | Czas liniowy (z dokładnością do sortowania): podwojenie liczby wpisów, długości nazw, liczby pól dodatkowych albo rozmiaru danych zwiększa czas `New` i odczytu najwyżej 2,5×. |
| SAFE-006 | Pamięć `New` zależy od liczby wpisów i długości ich nazw i pól, nie od rozmiaru danych; pamięć odczytu wpisu jest stała. Żadna alokacja nie ma rozmiaru wziętego z pola archiwum bez wcześniejszego porównania z limitem i z rozmiarem pliku. |
| SAFE-008 | Dane prywatne nie trafiają do repozytorium ani do pakietu dowodów; narzędzia nie czytają katalogów spoza repozytorium bez jawnego parametru. |
