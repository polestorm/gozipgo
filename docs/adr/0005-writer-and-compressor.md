# 0005 Pisarz i kompresor (etap 4)

Status: przyjęta (robocza, do potwierdzenia przez właściciela) · 2026-11-27

## Kontekst

Etap 4 dodaje zapis archiwów. Decyzja właściciela z 2026-11-27 w pytaniu 2: kompresor własny, nie `compress/flate`,
bo tylko własny gwarantuje te same bajty w każdej wersji Go (zasada D). Wymagania `requirements/writer.md`
przejrzano z APPNOTE 6.3.10 i RFC 1951 i przyjęto; poniżej decyzje, które przy tym zapadły.

## Decyzje

### Kompresor (`internal/deflate`)

1. **Jeden algorytm, stałe parametry, żadnych poziomów.** Dopasowania przez łańcuchy skrótów w oknie 32 KiB
   z odroczonym wyborem, tak jak opisuje to rozdział 4 RFC 1951. Parametry z pomiaru na danych generowanych
   (znaczniki, wiersze liczb, szum, powtórzenia, zera), zmienione 2026-11-27 decyzją właściciela tak, żeby zapis
   nie był wolniejszy niż w `archive/zip` (pkt 6):
   - skrót z czterech bajtów, nie z trzech: łańcuch nie zbiera przypadkowych trafień; dopasowań długości 3
     prawie nie szukamy;
   - najwyżej 32 kandydatów na pozycję, ćwierć tego, gdy odroczone dopasowanie ma już 8 bajtów; dopasowanie od
     64 bajtów jest wydawane bez sprawdzania następnej pozycji; dopasowanie długości 3 dalej niż 4096 bajtów
     jest pomijane;
   - z pozycji objętych dopasowaniem do łańcuchów trafiają tylko 64 ostatnie;
   - na danych bez dopasowań po każdych 32 kolejnych pozycjach bez trafienia o jedną pozycję więcej idzie
     literałem bez szukania, najwyżej 8 naraz; pierwsze trafienie zeruje licznik. Przeskok jest stanem kodera
     zależnym tylko od danych, więc nie narusza pkt 2.
   Pierwsza wersja (skrót z trzech bajtów, 128 kandydatów, każda pozycja w łańcuchu) dawała wynik o 5,6% mniejszy
   na znacznikach, ale zapis 2,2 raza wolniejszy niż `archive/zip`.
2. **Bajty wyjścia są kontraktem.** Zależą tylko od danych: nie od podziału na wywołania `Write` (pozycja jest
   kodowana, gdy ma przed sobą 263 bajty albo strumień się kończy), nie od tego, co koder kodował przed `Reset`,
   nie od kolejności map ani od architektury. Wzorce skrótów (`internal/deflate/testdata/golden.tsv`,
   `zipwriter/testdata/golden.tsv`) są wspólne dla wszystkich systemów. Zmiana parametru albo reguły remisu jest
   zmianą wyniku biblioteki: wymaga decyzji, `-update-golden` i wpisu w `CHANGELOG.md`.
3. **Blok ma najwyżej 16 384 symbole i 65 535 bajtów wejścia** i dostaje najtańszą z trzech postaci, liczoną
   dokładnie w bitach (przy remisie: niezakodowana, potem kody stałe). Granica bajtów ma dwa skutki. Blok zawsze
   mieści się w jednym bloku niezakodowanym i jego dane są jeszcze w buforze. Stosunek kompresji nie przekracza
   1000:1 (dane z samych zer: około 860:1), więc wpis zapisany pisarzem mieści się w domyślnym `MaxRatio`
   czytnika. Koszt: dane z jednego powtarzanego bajtu wychodzą o 19% większe niż z `compress/flate` (39 kB wobec
   33 kB na 32 MiB zer).
4. **Kody Huffmana wyznacza algorytm package-merge** (kod o najmniejszym koszcie przy limicie 15 bitów, 7 dla
   kodu długości), z remisami rozstrzyganymi stałą regułą. Test porównuje koszt z kodem Huffmana bez limitu.
5. **Kod jest zawsze pełny.** Gdy blok używa jednej odległości albo żadnej, a także gdy alfabet ma jeden symbol,
   długość 1 dostają dwa symbole. RFC 1951 dopuszcza dla odległości kod z jednym słowem; pełny kod przyjmuje
   każdy dekoder, także nasz, który niepełne kody znaków odrzuca (ADR 0003 pkt 10). Koszt to najwyżej kilka bitów
   na blok.
6. **Szybkość nie jest kryterium, ale jest zmierzona i ma cel: nie wolniej niż `archive/zip`.** Pomiar
   `tools/bench` (`comparison.md`, commit `435fc4e`, Apple M4): czas zapisu od 0,57 do 0,85 czasu `archive/zip`
   w postaci fabrycznej na danych kompresowanych (znaczniki 72 MB/s wobec 48 MB/s, wiersze liczb 55 wobec 47,
   szum 200 wobec 114, zera 1148 wobec 819, 100 000 małych wpisów 89 wobec 71). Rozmiar: archiwum znaczników praktycznie równe temu
   z `archive/zip` (7,26 MB wobec 7,27 MB) i 1,078 wyniku `compress/flate` na poziomie domyślnym, przy progu 1,1
   z ZW-006; wiersze liczb 1,045. `archive/zip`, mierzone jako czarna skrzynka, kompresuje słabiej niż poziom
   domyślny `compress/flate`, stąd dwa punkty odniesienia.
7. **Gdyby rozmiar stał się ważniejszy niż szybkość**, dochodzi nazwany poziom kompresji jako pole `Header`
   (np. `Level: zipwriter.Smaller`), a wartość zerowa zostaje dzisiejszym algorytmem. Każdy poziom to stały
   zestaw parametrów z własnym wzorcem skrótów, więc pkt 2 obowiązuje dla każdego z osobna, a istniejące archiwa
   i wzorce się nie zmieniają. Zmierzone możliwości: łańcuch 128 ze skrótem z czterech bajtów daje na znacznikach
   1,002 wyniku `compress/flate` przy czasie około 2 razy dłuższym niż `archive/zip`; dalej idzie już tylko
   dobór dopasowań z wyprzedzeniem (analiza kosztu całego bloku); to szacunek, nie pomiar: kilka razy wolniej,
   kilka procent mniej. Nie dodajemy tego, dopóki konsument nie poprosi.

### Pisarz (`zipwriter`)

8. **API: `New(w)`, `Add`, `Create`, `AddRaw`, `Close`.** Bez struktury opcji: nie ma dziś czego ustawiać. `Add`
   bierze całą treść i zapisuje rozmiary oraz CRC-32 w nagłówku lokalnym; `Create` zwraca `io.WriteCloser`
   i zapisuje je w deskryptorze danych z sygnaturą. Tak rozstrzygamy pytanie ze szkicu projektu, co jest
   domyślne: wybiera wywołujący przez wybór metody. Pierwszy wpis `mimetype` pakietów ODF i EPUB (Stored, bez
   deskryptora i bez pól dodatkowych) zapisuje `Add`.
9. **Wpis z `Create` zamyka się jawnie.** Następny wpis albo `Close` pisarza przy otwartym wpisie to
   `write-entry-open`. Pisarz nie zgaduje, że treść jest skończona: błąd w połowie strumienia nie zamieni się
   po cichu w krótszy plik.
10. **Stored z deskryptorem danych jest dozwolony** (`Create` z metodą Stored), bo nasz czytnik i wszystkie
   zmierzone programy czytają go poprawnie. Programy czytające archiwum strumieniem, bez katalogu, nie potrafią
   wyznaczyć końca takich danych; dokumentacja pakietu to mówi i wskazuje `Add`.
11. **ZIP64 tylko tam, gdzie trzeba.** W katalogu pole ZIP64 zawiera dokładnie wartości, które nie mieszczą się
    w 32 bitach; rekord i lokalizator końca katalogu pojawiają się od 65 535 wpisów albo 4 GiB. Nagłówek lokalny
    wpisu strumieniowego powstaje, zanim znany jest rozmiar, więc o jego postaci decyduje `Header.Zip64`
    (rozmiary graniczne, pole z dwoma zerami, deskryptor 64-bitowy). Bez niego wpis, który osiąga 4 GiB − 1
    bajtów, kończy się `write-entry-size` zamiast archiwum, którego część programów nie przeczyta. `Add`
    i `AddRaw` znają rozmiary i wybierają postać same.
12. **Pola stałe.** „Version made by” 4.5 i system 0 (MS-DOS), wersja wymagana według 4.4.3.2, atrybuty zerowe
    poza bitem katalogu, żadnych komentarzy, żadnych pól dodatkowych poza ZIP64. Uprawnień uniksowych nie
    zapisujemy; dojdą jako pole nagłówka, gdy poprosi o nie konsument.
13. **Data wpisu: z nagłówka albo 1980-01-01 00:00:00.** Nieparzysta sekunda i data spoza zakresu formatu to
    `write-timestamp`, nie ciche zaokrąglenie.
14. **Nazwy: te same reguły i kody co w czytniku**, ze wspólnego pakietu `internal/entryname`; konflikty nazw
    (ta sama nazwa, plik w miejscu katalogu) wykrywane przy wywołaniu, przez wyszukiwanie w zbiorach, bez
    iteracji po mapie. Porównania bez względu na wielkość liter pisarz nie robi: to reguła warstwy OPC
    u konsumenta.
15. **`AddRaw` sprawdza to, co kopiuje.** Strumień DEFLATE jest dekodowany w trakcie kopiowania, a rozmiar
    i CRC-32 treści porównywane z opisem; inaczej pisarz mógłby zapisać archiwum, którego czytnik nie przyjmie.
    Koszt to czas dekompresji (kilkaset MB/s), bez dodatkowej pamięci.
16. **Dwa rodzaje błędów.** Wykryty przed pierwszym bajtem wpisu (nazwa, metoda, data, stan) nie zmienia stanu
    pisarza i nie zostawia śladu w archiwum. Wykryty później (błąd celu zapisu, `write-entry-size`,
    `write-raw-content`) jest trwały: archiwum jest niedokończone i każde następne wywołanie zwraca ten sam błąd.
    Błąd `io.Writer` wraca niezmieniony, tak jak błąd `io.ReaderAt` w czytniku.
17. **K8 dla pisarza mierzą wzorce skrótów.** `TestGolden` w obu pakietach na trzech systemach CI z bieżącym
    wydaniem Go oraz zadanie `bytes` na wersji z `go.mod`. Lokalnie sprawdzone 2026-11-27 na go1.26.0, go1.26.6
    i go1.27.0 (darwin/arm64): te same skróty.

## Skutki

- ZW-001…010 przyjęte i pokryte testami; K4 obejmuje teraz 77 wymagań.
- Czarne skrzynki (ZW-007, `tools/compare`, 2026-11-27, macOS): `archive/zip`, 7-Zip, `zipfile` Pythona i bsdtar
  oddają treść wszystkich 7 archiwów pisarza; UnZip w wydaniu Apple 6 z 7 (nie tworzy plików o nazwach spoza
  ASCII, tak samo jak dla archiwów innych programów: `differences.md`).
- Rozmiar (ZW-006, `tools/bench`): 1,078 wyniku `compress/flate` na znacznikach i 1,045 na wierszach liczb
  (próg 1,1).
- Kampania fuzzingu dla wydania musi objąć `FuzzRoundTrip` i `FuzzDeflate`; zlecenie na commicie kandydata.
- Do decyzji właściciela: czy v0.3.0 (pisarz) wychodzi osobno po v0.2.0, czy jedno wydanie obejmuje etapy 3 i 4;
  czy 1.0 obejmuje pisarza (pytanie 10).
