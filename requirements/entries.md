# Wymagania: wpisy i ich treść

Status: szkic

| Id | Wymaganie |
|---|---|
| ENT-001 | Przesunięcie nagłówka lokalnego każdego wpisu wskazuje sygnaturę nagłówka lokalnego w pliku, przed katalogiem centralnym; inaczej `zip-local-header-mismatch`. |
| ENT-002 | Nagłówek lokalny zgadza się z katalogiem centralnym: metoda, flagi istotne dla odczytu (bity 0, 3, 11 i bity szyfrowania), nazwa. Bez bitu 3: CRC-32 i oba rozmiary równe katalogowi (z uwzględnieniem pola ZIP64). Z bitem 3: pola w nagłówku lokalnym zerowe albo równe katalogowi, a deskryptor danych po treści równy katalogowi. Niezgodność: `zip-local-header-mismatch` albo `zip-descriptor-mismatch`. |
| ENT-003 | Deskryptor danych może mieć sygnaturę albo nie, i pola 32- albo 64-bitowe; postać wynika z długości obszaru między końcem danych a początkiem następnego obszaru, nie ze zgadywania po wartościach. Długość nieodpowiadająca żadnej z czterech postaci daje `zip-descriptor-mismatch`. |
| ENT-004 | Obszary wpisów (nagłówek lokalny z nazwą i polami dodatkowymi, dane skompresowane, deskryptor) są rozłączne między sobą i z katalogiem; nachodzenie daje `zip-entries-overlap` z indeksami obu wpisów. Dwa wpisy katalogu wskazujące ten sam nagłówek lokalny to nachodzenie. |
| ENT-005 | Obszary pokrywają plik od bajtu zero do początku katalogu bez luk; dane przed pierwszym wpisem albo między wpisami dają `zip-unreferenced-data` z przesunięciem. Opcje `AllowPrefix` i `AllowGaps` (pytanie 3) wyłączają odpowiednio pierwszą i drugą część; ich zadziałanie jest raportowane przez `Relaxed()`. |
| ENT-006 | Stored: rozmiar skompresowany równa się rozmiarowi po rozpakowaniu; treść to bajty obszaru danych. |
| ENT-007 | Deflate: treść to wynik dekompresji obszaru danych; strumień musi kończyć się dokładnie na końcu obszaru (`inflate-trailing-data`, `inflate-truncated`). |
| ENT-008 | Odczyt treści daje dokładnie zadeklarowaną liczbę bajtów. Strumień dłuższy przerywa odczyt błędem `zip-size-mismatch` w chwili przekroczenia, krótszy przy końcu strumienia. |
| ENT-009 | CRC-32 treści jest liczone w czasie odczytu; niezgodność daje `zip-checksum` przy odczycie, który inaczej zwróciłby `io.EOF`. Odczyt przerwany przez wywołującego przed końcem nie potwierdza integralności; dokumentacja `Open` to mówi. |
| ENT-010 | `Open` nie czyta żadnego bajtu spoza obszaru danych wpisu; `OpenRaw` wydaje bajty obszaru danych bez dekompresji i bez sprawdzania CRC. |
| ENT-011 | Wpis z szyfrowaniem albo nieobsługiwaną metodą jest na liście; `Open` daje `unsupported-encryption` albo `unsupported-method`, a `OpenRaw` działa. |
| ENT-012 | `Open` można wywołać wiele razy i równolegle dla różnych i tych samych wpisów (pytanie 12); każde wywołanie daje niezależny czytnik. `Close` czytnika wpisu nie zamyka archiwum. |
| ENT-013 | Opcja odroczonego sprawdzania nagłówków lokalnych, jeśli powstanie (design.md), nie zmienia zbioru archiwów przyjętych po przeczytaniu wszystkich wpisów: różni się tylko chwilą zgłoszenia błędu. |
