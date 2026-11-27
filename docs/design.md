# Projekt

Szkic architektury z 2026-11-26. Sygnatury są planem do potwierdzenia testami w etapach; wiążące są wymagania
w `../requirements/` i decyzje w `adr/`.

## Pakiety

```
internal/format     stałe i struktury rekordów ZIP (sygnatury, rozmiary pól, bity flag), bez logiki
internal/entryname  reguły nazw wpisów wspólne dla czytnika i pisarza
internal/inflate    dekompresja DEFLATE według RFC 1951 (pytanie 1)
internal/deflate    kompresja DEFLATE o ustalonym algorytmie (pytanie 2, ADR 0005)
zipreader           czytnik: otwarcie, lista wpisów, treść wpisu, limity, kody błędów
zipwriter           pisarz (ADR 0005)
```

Z biblioteki standardowej pakiety biblioteki importują tylko to, co nie ma wejścia ani wyjścia poza interfejsami
`io`: `io`, `errors`, `hash/crc32`, `encoding/binary`, `math/bits`, `unicode/utf8`, `sort`, `strconv`, `strings`,
`sync`, `sync/atomic`. Nigdy `archive/zip`,
`os`, `net` ani `fmt` (wciąga `os`); `compress/flate` tylko w testach. Listę trzyma `internal/project/api_test.go`.

## Czytnik

```go
r, err := zipreader.New(ra io.ReaderAt, size int64, opts zipreader.Options)
r.Entries() []Entry            // kolejność katalogu centralnego; wycinek tylko do odczytu
r.Comment() string
r.Relaxed() []Relaxation       // które poluzowania zadziałały (puste w trybie ścisłym)
// etap 1 (ADR 0002): Options.UnflaggedNames (ASCIIOnly, CP437, UTF8, CodePage z tablicą
// Options.CodePage) zamiast AllowCP437; Options.FoldCase;
// Entry.Unsupported: kod, który zwróci Open dla wpisu z cechą spoza zakresu
// etap 2 (ADR 0003): Options.MaxEntrySize, MaxTotalSize, MaxRatio, AllowPrefix; Entry.DataOffset;
// ADR 0006: Options.AllowVersionHostByte, AllowUnusedZip64 (domyślnie wyłączone, raportowane przez Relaxed)
// ErrNoEntry dla wpisu spoza czytnika, ErrClosed dla odczytu po Close
rc, err := r.Open(e)           // io.ReadCloser treści wpisu
raw, err := r.OpenRaw(e)       // dane skompresowane, bez dekompresji (kopiowanie wpisu do innego archiwum)
```

`Entry`: nazwa, znacznik katalogu, metoda, rozmiar skompresowany i po rozpakowaniu, CRC-32, flagi, data i czas
w postaci zapisanej w archiwum (pola liczbowe, bez strefy czasowej i bez `time.Local`), komentarz, pola dodatkowe
jako surowe bajty, przesunięcia obszaru wpisu w pliku, indeks w katalogu.

### Otwarcie archiwum (`New`): kolejność kroków

1. **Koniec katalogu centralnego.** Szukany od końca pliku w oknie 22 + 65 535 bajtów. Musi być dokładnie jeden
   kandydat, którego długość komentarza kończy się równo z plikiem. Numery dysków muszą być zerowe.
2. **ZIP64.** Gdy któreś pole końca katalogu ma wartość graniczną (0xFFFF, 0xFFFFFFFF), wymagany jest lokalizator
   ZIP64 tuż przed końcem katalogu i rekord końca katalogu ZIP64 tam, gdzie lokalizator wskazuje; ich pola muszą
   się zgadzać z rekordem zwykłym tam, gdzie ten nie ma wartości granicznej. Lokalizator bez potrzeby jest
   dozwolony tylko wtedy, gdy wartości są spójne.
3. **Limity przed alokacją.** Liczba wpisów i rozmiar katalogu są porównywane z limitami i z rozmiarem pliku
   (każdy wpis katalogu ma co najmniej 46 bajtów), zanim cokolwiek zostanie zaalokowane.
4. **Katalog centralny.** Czytany strumieniowo; każdy nagłówek sprawdzony (sygnatura, wersja wymagana, flagi,
   metoda, długości zmienne w granicach katalogu, pole dodatkowe ZIP64, nazwa). Liczba przeczytanych nagłówków
   i ich łączny rozmiar muszą się równać wartościom z końca katalogu.
5. **Nazwy.** Reguły z ADR 0001 pkt 5; kodowanie: z bitem 11 poprawne UTF-8, bez bitu 11 tylko ASCII (opcja
   `AllowCP437` dekoduje według tablicy). Wykrywanie powtórzeń przez sortowanie, nie przez mapę z iteracją.
6. **Nagłówki lokalne i układ pliku.** Dla każdego wpisu czytany jest nagłówek lokalny i porównywany z katalogiem;
   wyznaczany jest obszar wpisu (nagłówek, dane, deskryptor). Obszary posortowane po przesunięciu muszą być
   rozłączne, przylegać do siebie bez luk, zaczynać się na początku pliku i kończyć na początku katalogu
   (albo rekordów ZIP64).

Krok 6 kosztuje jeden mały odczyt na wpis (przez bufor, w kolejności położenia). Opcji odraczającej porównanie
nagłówków do `Open` nie ma (ADR 0003 pkt 1 i 9): po `New` układ pliku jest sprawdzony w całości.

### Treść wpisu (`Open`)

- czytnik ograniczony do obszaru danych wpisu (`io.SectionReader`), więc nie przeczyta bajtu spoza niego;
- Stored: rozmiary skompresowany i po rozpakowaniu muszą być równe;
- Deflate: własny dekoder; koniec strumienia musi wypaść dokładnie na końcu danych skompresowanych;
- licznik bajtów wyjścia: przekroczenie rozmiaru zadeklarowanego, limitu wpisu, limitu łącznego albo stosunku
  kompresji kończy odczyt błędem natychmiast, nie na końcu;
- CRC-32 liczone w locie; niezgodność albo krótszy strumień to błąd przy ostatnim odczycie zamiast `io.EOF`.
  Wywołujący, który nie doczytał do końca, nie ma potwierdzenia integralności i dokumentacja to mówi;
- deskryptor danych (bit 3): po danych czytany i porównywany z katalogiem; cztery dozwolone postaci (z sygnaturą
  albo bez, 32 albo 64 bity) rozstrzyga długość obszaru wyznaczona w `New`, nie zgadywanie.

Limit łączny dotyczy sumy bajtów wydanych przez wszystkie `Open` jednego czytnika; ponowne otwarcie tego samego
wpisu liczy się ponownie (ochrona przed wielokrotnym rozpakowaniem tej samej bomby).

### Dlaczego tak ściśle

Archiwum ZIP opisuje każdy plik dwa razy i pozwala trzymać dane, na które nic nie wskazuje. Programy rozstrzygają
niezgodności różnie: jedne ufają katalogowi, inne czytają nagłówki lokalne od początku pliku. Plik zbudowany tak,
żeby skaner widział jedną zawartość, a program docelowy inną, jest poprawny dla obu. Jedyna obrona po stronie
czytnika to przyjąć tylko archiwa, dla których wszystkie interpretacje dają to samo. Stąd reguła „jedna
interpretacja albo odrzucenie” i brak czytania strumieniem do przodu.

## Dekompresja (`internal/inflate`)

RFC 1951: bloki niezakodowane, ze stałymi i z dynamicznymi kodami Huffmana; okno 32 KiB. Wymagania szczególne:

- każdy błąd strumienia (zarezerwowany typ bloku, niepełny albo nadmiarowy zestaw kodów, odległość poza
  dotychczasowe wyjście, niezgodność LEN i NLEN) to stały kod, nie panika;
- pamięć stała (okno i tablice kodów), niezależna od danych;
- czytnik bitów nie czyta poza koniec danych skompresowanych i mówi, ile bajtów wejścia zużył;
- test różnicowy z `compress/flate`: te same bajty wyjścia dla każdego poprawnego strumienia, błąd dla każdego
  niepoprawnego (różnice w tym, co jest błędem, opisane w `differences.md`).

## Pisarz (`zipwriter`)

Stan po etapie 4 (ADR 0005); wiążące są `requirements/writer.md` i wzorzec `api/zipwriter.txt`.

```go
w := zipwriter.New(out io.Writer)
err := w.Add(zipwriter.Header{Name: "word/document.xml", Method: zipwriter.Deflate}, content)
ew, err := w.Create(zipwriter.Header{Name: "big.bin", Method: zipwriter.Deflate}) // io.WriteCloser
err = w.AddRaw(header, raw io.Reader, zipwriter.RawInfo{CompressedSize, UncompressedSize, CRC32})
err = w.Close()
```

- kolejność wpisów taka jak wywołań; nazwy sprawdzane tymi samymi regułami co w czytniku (`internal/entryname`),
  z tymi samymi kodami;
- data i czas tylko z nagłówka podanego przez wywołującego; domyślnie 1980-01-01 00:00:00; nigdy zegar;
- żadnych pól dodatkowych poza ZIP64 i żadnych komentarzy; pola stałe opisane w ZW-003;
- zapis do `io.Writer` bez cofania: `Add` zna treść, więc rozmiary i CRC-32 idą do nagłówka lokalnego (dla Deflate
  treść skompresowana czeka w pamięci); `Create` zapisuje je w deskryptorze danych (bit 3); `AddRaw` dostaje je
  od wywołującego i sprawdza w trakcie kopiowania;
- wpis z `Create` zamyka się jawnie; pisarz nie zgaduje, że poprzedni wpis jest skończony;
- ZIP64 tylko tam, gdzie wartość tego wymaga; dla wpisu strumieniowego, którego rozmiar nie jest znany przy
  zapisie nagłówka, decyduje `Header.Zip64`;
- błąd wykryty przed pierwszym bajtem wpisu nie zmienia stanu; błąd późniejszy (cel zapisu, rozmiar, dane surowe)
  jest trwały, bo archiwum nie da się dokończyć;
- to, co pisarz zapisał, czytnik przyjmuje w trybie ścisłym, a czarne skrzynki rozpakowują z tą samą treścią.

## Kompresja (`internal/deflate`)

RFC 1951, rozdziały 3 i 4. Jeden algorytm, stałe parametry, żadnych poziomów:

- dopasowania przez łańcuchy skrótów czterech bajtów w oknie 32 KiB, najwyżej 32 kandydatów na pozycję, z odroczonym
  wyborem (dopasowanie krótsze niż 64 bajty jest wydawane dopiero, gdy następna pozycja nie daje dłuższego);
  na danych bez dopasowań koder przeskakuje do 8 pozycji bez szukania (parametry i ich pomiar: ADR 0005 pkt 1);
- pozycja jest kodowana, gdy ma przed sobą 263 bajty danych albo strumień się kończy, więc wynik nie zależy od
  podziału danych na zapisy;
- blok kończy się po 16 384 symbolach albo 65 535 bajtach wejścia i dostaje najtańszą z trzech postaci
  (niezakodowana, kody stałe, kody dynamiczne), liczoną dokładnie w bitach;
- kody dynamiczne o najmniejszym koszcie przy limicie 15 bitów (7 dla kodu długości) wyznacza algorytm
  package-merge; kod jest zawsze pełny;
- pamięć stała (około 0,5 MiB na koder), bez alokacji w czasie pracy.

## Kody błędów

Jeden typ `Error` w pakiecie: `Code` (stała), `Entry` (indeks w katalogu albo −1), `Name`, `Offset` (w pliku),
`Message` (po angielsku, nie jest kontraktem). Rodziny kodów (lista robocza, katalog w `docs/errors.md` od etapu 1):

| Rodzina | Znaczenie | Przykłady |
|---|---|---|
| `zip-*` | naruszenie formatu albo niespójność struktur | `zip-no-directory-end`, `zip-multiple-directory-ends`, `zip-trailing-data`, `zip-directory-out-of-bounds`, `zip-directory-count-mismatch`, `zip-local-header-mismatch`, `zip-entries-overlap`, `zip-unreferenced-data`, `zip-descriptor-mismatch`, `zip64-inconsistent`, `zip-checksum`, `zip-size-mismatch` |
| `name-*` | nazwa wpisu | `name-empty`, `name-traversal`, `name-absolute`, `name-backslash`, `name-nul`, `name-duplicate`, `name-encoding` |
| `inflate-*` | strumień DEFLATE | `inflate-block-type`, `inflate-code-lengths`, `inflate-distance`, `inflate-stored-length`, `inflate-trailing-data`, `inflate-truncated` |
| `limit-*` | limity czytnika | `limit-entries`, `limit-directory-size`, `limit-name-length`, `limit-entry-size`, `limit-total-size`, `limit-ratio` |
| `unsupported-*` | poprawne, ale poza zakresem | `unsupported-encryption`, `unsupported-method`, `unsupported-multi-volume`, `unsupported-version`, `unsupported-patch` |

## Czego biblioteka nie robi

- nie rozpakowuje na dysk (pytanie 6);
- nie naprawia i nie zgaduje: archiwum bez końca katalogu nie jest „odzyskiwane” z nagłówków lokalnych;
- nie zna OPC: typy treści, relacje i równoważność nazw bez względu na wielkość liter należą do warstwy pakietu
  u konsumenta (`godocxgo/internal/opc`, później wspólny moduł). `gozipgo` daje jej listę wpisów i treść.
