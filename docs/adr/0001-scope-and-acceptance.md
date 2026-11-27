# 0001 Zakres i kryteria gotowości

Status: przyjęta (robocza) · 2026-11-26

## Kontekst

`godocxgo` czyta dokumenty Worda, czyli pakiety OPC w archiwum ZIP, z wejścia niezaufanego; to samo czeka czytnik
XLSX i każdy import paczek z zewnątrz. Biblioteka standardowa Go ma `archive/zip`. Sprawdzenie jej źródeł
(Go 1.26, 2026-11-26) pokazało, czego nie daje:

- **brak limitów**: liczby wpisów, łącznego rozmiaru po rozpakowaniu, stosunku kompresji. Rozmiar wpisu jest
  pilnowany względem pola nagłówka, które pisze atakujący;
- **ścieżki `../` i bezwzględne przechodzą domyślnie** (kontrola tylko po ustawieniu `GODEBUG=zipinsecurepath=0`);
- **powtórzone nazwy przechodzą**: lista plików zawiera oba wpisy;
- **katalog centralny nie jest porównywany z nagłówkami lokalnymi**, wpisy nachodzące na siebie i dane, na które
  nie wskazuje żaden wpis, nie są wykrywane. To są techniki bomb ZIP nowszej generacji i przemycania treści
  (różne programy widzą w tym samym pliku różną zawartość);
- **błędy bez stałych kodów**, a zachowanie zmienia się między wydaniami Go, więc część zachowania walidowanego
  u odbiorcy zależałaby od wersji kompilatora;
- **zapis**: bajty skompresowane przez `compress/flate` mogą się zmienić z wersją Go, co łamie zasadę powtarzalności
  rodziny („te same bajty w obrębie wersji biblioteki”).

Każdą z tych luk da się załatać nakładką, ale nakładka musi sama czytać koniec katalogu (limit wpisów trzeba
sprawdzić, zanim `archive/zip` zaalokuje pamięć) i nagłówki lokalne. Do własnego czytnika kontenera brakuje wtedy
tylko katalogu centralnego. **Decyzja właściciela z 2026-11-26: własna implementacja, bez zależności od
`archive/zip`.**

Decyzja doprecyzowuje literę I zasad ROADIE rodziny: niezależność od źródeł nieznanych, nietestowanych,
niezaufanych albo niewalidowalnych. `archive/zip` jest znany i testowany, ale nie da się go zwalidować względem
naszych wymagań bez przepisania połowy jego pracy.

## Decyzja

1. **Osobny moduł `github.com/polestorm/gozipgo`**, publiczny, bez zależności. Pakiety: `zipreader` (etapy 1–3),
   `zipwriter` (etap 4), wspólne typy formatu w pakiecie wewnętrznym. Nazwy jak w `goxmlgo`.
2. **Źródła:** APPNOTE.TXT (PKWARE, .ZIP File Format Specification, wersja przypięta w `../provenance.md`),
   ISO/IEC 21320-1:2015 (Document Container File: profil ZIP używany przez OPC, ODF i EPUB), RFC 1951 (DEFLATE),
   definicja CRC-32 z APPNOTE. Kodu innych implementacji nie czytamy, także `archive/zip` i `compress/flate`.
3. **Zakres formatu:** nagłówek lokalny, deskryptor danych (z sygnaturą i bez, 32- i 64-bitowy), nagłówek
   katalogu centralnego, koniec katalogu centralnego, ZIP64 (koniec katalogu, lokalizator, pole dodatkowe
   0x0001), metody Stored (0) i Deflate (8), nazwy w UTF-8 (bit 11) i ASCII, wpisy katalogów, komentarze. Pola
   dodatkowe: sprawdzana struktura, rozumiane ZIP64, pozostałe wydawane wywołującemu jako surowe bajty.
4. **Poza zakresem, wykrywane i odrzucane ze stałym kodem:** szyfrowanie (tradycyjne, silne, AES), archiwa
   wieloczęściowe, metody inne niż 0 i 8 (Deflate64, bzip2, LZMA, zstd i pozostałe), dane łatek, szyfrowany
   katalog centralny, wersja wymagana powyżej 4.5. Naprawianie uszkodzonych archiwów i czytanie bez katalogu
   centralnego (samym strumieniem do przodu) nie są planowane: katalog centralny jest jedynym źródłem listy wpisów.
5. **Jedna interpretacja albo odrzucenie.** Archiwum jest przyjmowane tylko wtedy, gdy:
   - jest dokładnie jeden koniec katalogu centralnego, a po nim tylko komentarz o zadeklarowanej długości;
   - katalog centralny mieści się w pliku, ma zadeklarowaną liczbę wpisów i rozmiar;
   - nagłówek lokalny każdego wpisu zgadza się z katalogiem (nazwa, metoda, flagi, rozmiary i CRC albo deskryptor
     danych, który się z nimi zgadza);
   - obszary wpisów (nagłówek lokalny, dane, deskryptor) nie nachodzą na siebie ani na katalog;
   - między obszarami nie ma danych, na które nic nie wskazuje (ani przed pierwszym wpisem);
   - nazwy są niepuste, bez znaku NUL i odwrotnego ukośnika, nie bezwzględne, bez segmentów `..` i `.`, i nie
     powtarzają się;
   - strumień DEFLATE kończy się dokładnie na końcu danych skompresowanych, daje dokładnie zadeklarowaną liczbę
     bajtów i zadeklarowane CRC-32.
   Poluzowania (dane przed archiwum dla archiwów samorozpakowujących, luki, nazwy w CP437) to nazwane opcje,
   domyślnie wyłączone; ich użycie jest widoczne w wyniku.
6. **Limity z bezpiecznymi wartościami domyślnymi**, liczone na danych rzeczywistych: liczba wpisów, rozmiar
   katalogu centralnego, długość nazwy, komentarza i pól dodatkowych, rozmiar wpisu po rozpakowaniu, łączny rozmiar
   po rozpakowaniu, stosunek kompresji. Zero oznacza wartość domyślną, wartość ujemna brak limitu (jak w `goxmlgo`).
   Wartości domyślne: pytanie 9.
7. **Bez systemu plików i sieci.** Wejście: `io.ReaderAt` i rozmiar. Wyjście: lista wpisów w kolejności katalogu
   centralnego i `io.ReadCloser` treści wpisu. Biblioteka nie tworzy plików; rozpakowanie na dysk jest sprawą
   wywołującego (pytanie 6).
8. **Dekompresja DEFLATE:** własna, według RFC 1951, w pakiecie wewnętrznym (rekomendacja; pytanie 1 potwierdza
   przed etapem 2). `compress/flate` jest wyrocznią w testach. CRC-32 z `hash/crc32`: wynik jest w całości
   wyznaczony przez definicję i sprawdzany wektorami testowymi.
9. **Pisarz (etap 4):** kolejność wpisów podana przez wywołującego, żadnych znaczników czasu poza jawnie podanymi
   (domyślnie stała data), żadnych pól dodatkowych poza ZIP64, gdy jest konieczny; metody Stored i Deflate
   z własnym kompresorem o ustalonym algorytmie, tak żeby te same wywołania dawały te same bajty w każdej wersji
   Go (pytanie 2). Wynik pisarza musi być przyjmowany przez czytnik w trybie ścisłym i przez czarne skrzynki.
10. **Błędy:** typ `Error` ze stałym kodem, indeksem i nazwą wpisu (gdy dotyczy) oraz przesunięciem w pliku.
    Katalog kodów w `../errors.md`; kod raz wydany nie zmienia znaczenia.

## Kryteria gotowości

Wszystkie muszą być spełnione jednocześnie, zmierzone i zapisane z datą w `../testing.md`:

| # | Kryterium | Miara |
|---|---|---|
| K1 | inwentarz formatu | każdy rekord i każde pole z rozdziałów APPNOTE w zakresie, każdy z 16 bitów flag ogólnych, każda metoda kompresji z listy APPNOTE i każdy identyfikator pola dodatkowego z listy ma w `requirements/` przypisaną obsługę albo kod odrzucenia; test buduje archiwum z każdym z nich i sprawdza werdykt |
| K2 | korpus zgodności | 100% archiwów korpusu (utworzonych przez wymienione programy, z manifestem: program, wersja, polecenie, SHA-256 każdego wpisu) czytanych w trybie ścisłym z listą i treścią zgodną z manifestem; wyjątek tylko wymieniony w `../differences.md` z powodem |
| K3 | czarne skrzynki | na korpusie zgodności ta sama lista nazw i te same SHA-256 treści co `archive/zip`, Info-ZIP (`unzip`), 7-Zip i `zipfile` Pythona, 100%; na korpusie wrogim każde archiwum odrzucone z kodem oczekiwanym w manifeście, a zachowanie czarnych skrzynek zapisane w `../comparison.md`; każda różnica w `../differences.md` |
| K4 | wymagania i API | każde przyjęte wymaganie ma test i każde `REQ:` wskazuje istniejące wymaganie; publiczne API równe wzorcowi `api/*.txt` |
| K5 | odporność | fuzzery bez paniki i bez naruszenia niezmienników przez ≥ 24 h łącznie na kandydacie do wydania. Niezmienniki: archiwum przyjęte ma wpisy rozłączne i nazwy poprawne; treść wpisu ma zadeklarowany rozmiar i CRC albo odczyt kończy się błędem; archiwum przyjęte w trybie ścisłym `archive/zip` czyta z tą samą listą i treścią; własna dekompresja daje te same bajty co `compress/flate` albo oba zgłaszają błąd |
| K6 | bezpieczeństwo | zestaw wrogich archiwów odrzucony, każde w < 1 s i < 64 MB (lista w `requirements/safety.md`); pakiety biblioteki nie importują `os`, `net`, `archive/zip`; podwojenie rozmiaru wejścia patologicznego zwiększa czas najwyżej 2,5× |
| K7 | wydajność i pamięć | rozpakowanie Deflate nie wolniejsze niż 1,5× `archive/zip` na tych samych danych (cel roboczy, do potwierdzenia w etapie 3); otwarcie archiwum o 100 000 wpisów w < 0,5 s; pamięć na wpis zmierzona i zapisana; archiwum ZIP64 z wpisem > 4 GB (wejście syntetyczne, bez pliku na dysku) czytane w < 64 MB pamięci procesu |
| K8 | powtarzalność i konsument | 10 przebiegów daje te same listy, treści i kody błędów; wzorce korpusu zgodne na Linux, macOS i Windows; pisarz daje te same bajty w najniższej i najnowszej wersji Go z CI; `godocxgo` czyta swój korpus przez `gozipgo` bez zmian w `gozipgo` |
| K9 | dowód | pakiet dowodów: wymagania → testy → wynik ostatniego przebiegu, tabela K1–K9 ze źródłem każdej liczby, powtarzalny poza datą |

## Konsekwencje

+ zachowanie wobec wrogich archiwów jest nasze, opisane wymaganiami i niezależne od wersji Go;
+ jedna implementacja ZIP dla całej rodziny, z własnym korpusem, fuzzingiem i pakietem dowodów;
+ pisarz z bajtami niezależnymi od kompilatora (zasada D);
− ok. 2,5–3,5 tys. linii własnego kodu do utrzymania zamiast wywołań biblioteki standardowej;
− czytnik ścisły odrzuci archiwa, które inne programy otwierają (z danymi przed archiwum, z lukami, z nazwami
  w CP437): każde takie znalezisko w korpusie to wpis w `../differences.md` i decyzja, czy dodać opcję;
− własna dekompresja może być wolniejsza od biblioteki standardowej; K7 wyznacza granicę.

## Uzupełnienia z 2026-11-27 (decyzje właściciela)

- **K3 liczy program, nie jedno jego wydanie z wadą.** Gdy czarna skrzynka nie oddaje treści archiwum z powodu
  własnej wady, której inne wydanie tego samego programu nie ma, a treść czytana przez `gozipgo` zgadza się
  z manifestem, różnica jest opisywana w `differences.md` i nie rozstrzyga kryterium. Przypadek, który to wywołał:
  UnZip 6.00 w wydaniu Apple nie tworzy plików o nazwach spoza ASCII (18 z 22 na macOS), a w wydaniu Debiana czyta
  wszystkie. To samo dotyczy scalania wpisów `__MACOSX` przez bsdtar na macOS.
- **Wersja 1.0 obejmuje czytnik i pisarza.** Pisarz powstał przed odbiorem (etap 4), więc nie ma powodu wydawać go
  osobno w 1.1.
- **Tryb ścisły jest jedynym trybem domyślnym.** Poluzowania dochodzą jako nazwane opcje wtedy, gdy pokaże je
  potrzeba konsumenta (ADR 0006); tryb odzyskiwania uszkodzonych archiwów nie jest dziś w zakresie i może dojść
  w przyszłości jako osobne wejście, jeśli zajdzie potrzeba.

