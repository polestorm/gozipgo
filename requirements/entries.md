# Wymagania: wpisy i ich treść

Status: przyjęte

Przyjęte w etapie 2 (2026-11-27) po przeglądzie z APPNOTE 6.3.10 i na archiwach korpusu; decyzje w ADR 0003.
„Obszar wpisu” to nagłówek lokalny z nazwą i polami dodatkowymi, dane o rozmiarze skompresowanym z katalogu i,
z bitem 3 flag, deskryptor danych.

| Id | Wymaganie |
|---|---|
| ENT-001 | Przesunięcie nagłówka lokalnego każdego wpisu (APPNOTE 4.4.16) zostawia miejsce na nagłówek przed katalogiem centralnym i wskazuje sygnaturę nagłówka lokalnego (4.3.7); inaczej `zip-local-header-mismatch`. |
| ENT-002 | Nagłówek lokalny zgadza się z katalogiem centralnym: całe pole flag (4.4.4), metoda (4.4.5), nazwa (NAME-006). Bez bitu 3: CRC-32 i oba rozmiary równe katalogowi, z uwzględnieniem pola ZIP64. Z bitem 3: każde z tych pól zerowe albo równe katalogowi (4.4.4), a deskryptor danych równy katalogowi. Pole ZIP64 nagłówka lokalnego (4.5.3) jest wymagane przy wartości granicznej rozmiaru, zawiera dokładnie oba rozmiary, a pole rozmiaru bez wartości granicznej musi mu być równe (`zip64-inconsistent`). Pola dodatkowe mają poprawną strukturę (`zip-extra-field`); Unicode Path, jeśli jest, daje nazwę wpisu (`name-unicode-path`); pola szyfrowania i łatki nie mogą wystąpić przy wpisie, którego katalog tak nie oznacza. Wersja wymagana oraz data i czas nie są porównywane: nie wpływają na odczyt, a prawdziwe programy zapisują je różnie. Niezgodność: `zip-local-header-mismatch` albo `zip-descriptor-mismatch`. |
| ENT-003 | Deskryptor danych (4.3.9) może mieć sygnaturę albo nie i pola 4- albo 8-bajtowe; postać wynika z liczby bajtów między końcem danych a początkiem następnego obszaru (12, 16, 20 albo 24), nie ze zgadywania po wartościach. Inna liczba bajtów albo brak sygnatury w postaci 16- lub 24-bajtowej daje `zip-descriptor-mismatch`. |
| ENT-004 | Obszary wpisów są rozłączne między sobą i z katalogiem: nagłówek lokalny z danymi, który sięga w następny obszar (w kolejności położenia) albo w katalog, daje `zip-entries-overlap` z indeksami obu wpisów. Dwa wpisy katalogu wskazujące ten sam nagłówek lokalny to nachodzenie. |
| ENT-005 | Obszary pokrywają plik od bajtu zero do początku katalogu bez luk: dane przed pierwszym wpisem, po danych wpisu bez bitu 3 albo przed katalogiem archiwum bez wpisów dają `zip-unreferenced-data` z przesunięciem. Opcja `AllowPrefix` (pytanie 3) przyjmuje dane przed pierwszym wpisem w obu sposobach liczenia przesunięć (od początku pliku albo od końca danych wstępnych; który obowiązuje, wynika z położenia katalogu i rekordu ZIP64); jej zadziałanie jest raportowane przez `Relaxed()`. Luk między wpisami żadna opcja nie przyjmuje. |
| ENT-006 | Stored: rozmiar skompresowany równa się rozmiarowi po rozpakowaniu (`zip-size-mismatch` z `New`); treść to bajty obszaru danych. |
| ENT-007 | Deflate: treść to wynik dekompresji obszaru danych; strumień musi kończyć się dokładnie na końcu obszaru (`inflate-trailing-data`, `inflate-truncated`). |
| ENT-008 | Odczyt treści daje dokładnie zadeklarowaną liczbę bajtów. Strumień dłuższy przerywa odczyt błędem `zip-size-mismatch` po wydaniu zadeklarowanej liczby bajtów, krótszy przy końcu strumienia. |
| ENT-009 | CRC-32 treści (4.4.7) jest liczone w czasie odczytu; niezgodność daje `zip-checksum` przy odczycie, który inaczej zwróciłby `io.EOF`. Odczyt przerwany przez wywołującego przed końcem nie potwierdza integralności; dokumentacja `Open` to mówi. |
| ENT-010 | `Open` nie czyta żadnego bajtu spoza obszaru danych wpisu; `OpenRaw` wydaje bajty obszaru danych bez dekompresji i bez sprawdzania CRC. |
| ENT-011 | Wpis z szyfrowaniem, łatką, nieobsługiwaną metodą albo wersją jest na liście; `Open` daje kod z `Entry.Unsupported`, a `OpenRaw` działa. |
| ENT-012 | `Open` można wywołać wiele razy i równolegle dla różnych i tych samych wpisów (pytanie 12); każde wywołanie daje niezależny czytnik. `Close` czytnika wpisu nie zamyka archiwum; odczyt po `Close` daje `ErrClosed`. `Entry`, który nie należy do czytnika, daje `ErrNoEntry`. |

Wycofane: ENT-013 (opcja odroczonego sprawdzania nagłówków lokalnych). Opcja nie powstała: `New` czyta nagłówki
lokalne zawsze (ADR 0003 pkt 9).
