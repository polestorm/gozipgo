# 0005 Pisarz i kompresor (etap 4)

Status: przyjęta (robocza, do potwierdzenia przez właściciela) · 2026-11-27

## Kontekst

Etap 4 dodaje zapis archiwów. Decyzja właściciela z 2026-11-27 w pytaniu 2: kompresor własny, nie `compress/flate`,
bo tylko własny gwarantuje te same bajty w każdej wersji Go (zasada D). Wymagania `requirements/writer.md`
przejrzano z APPNOTE 6.3.10 i RFC 1951 i przyjęto; poniżej decyzje, które przy tym zapadły.

## Decyzje

### Kompresor (`internal/deflate`)

1. **Jeden algorytm, stałe parametry, żadnych poziomów.** Dopasowania przez łańcuchy skrótów trzech bajtów
   w oknie 32 KiB z odroczonym wyborem, tak jak opisuje to rozdział 4 RFC 1951. Parametry dobrane pomiarem na
   danych generowanych (znaczniki, wiersze liczb, powtórzenia, dokumenty tego repozytorium): najwyżej 128
   kandydatów na pozycję, ćwierć tego, gdy odroczone dopasowanie ma już 16 bajtów; dopasowanie długości 3 dalej
   niż 4096 bajtów jest pomijane. Mniejszy łańcuch (64) dawał o 4% większy wynik na znacznikach, większy (256)
   dwa razy dłuższy czas.
2. **Bajty wyjścia są kontraktem.** Zależą tylko od danych: nie od podziału na wywołania `Write` (pozycja jest
   kodowana, gdy ma przed sobą 262 bajty albo strumień się kończy), nie od tego, co koder kodował przed `Reset`,
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
6. **Szybkość nie jest kryterium, ale jest zmierzona** (`comparison.md`, 2026-11-27, Apple M4): zapis znaczników
   21 MB/s wobec 22 MB/s `compress/flate` na poziomie domyślnym i 46 MB/s `archive/zip` w postaci fabrycznej
   (która, mierzona jako czarna skrzynka, kompresuje słabiej niż poziom domyślny `compress/flate`: archiwum
   7,27 MB wobec 6,73 MB; nasze 6,87 MB); wiersze liczb 20 MB/s wobec 33 MB/s; dane niekompresowalne 56 MB/s
   wobec 122 MB/s. Czas schodzi w przeszukiwaniu łańcuchów. Przyspieszenie, które zmienia dobór dopasowań,
   zmienia bajty, więc podlega pkt 2.

### Pisarz (`zipwriter`)

7. **API: `New(w)`, `Add`, `Create`, `AddRaw`, `Close`.** Bez struktury opcji: nie ma dziś czego ustawiać. `Add`
   bierze całą treść i zapisuje rozmiary oraz CRC-32 w nagłówku lokalnym; `Create` zwraca `io.WriteCloser`
   i zapisuje je w deskryptorze danych z sygnaturą. Tak rozstrzygamy pytanie ze szkicu projektu, co jest
   domyślne: wybiera wywołujący przez wybór metody. Pierwszy wpis `mimetype` pakietów ODF i EPUB (Stored, bez
   deskryptora i bez pól dodatkowych) zapisuje `Add`.
8. **Wpis z `Create` zamyka się jawnie.** Następny wpis albo `Close` pisarza przy otwartym wpisie to
   `write-entry-open`. Pisarz nie zgaduje, że treść jest skończona: błąd w połowie strumienia nie zamieni się
   po cichu w krótszy plik.
9. **Stored z deskryptorem danych jest dozwolony** (`Create` z metodą Stored), bo nasz czytnik i wszystkie
   zmierzone programy czytają go poprawnie. Programy czytające archiwum strumieniem, bez katalogu, nie potrafią
   wyznaczyć końca takich danych; dokumentacja pakietu to mówi i wskazuje `Add`.
10. **ZIP64 tylko tam, gdzie trzeba.** W katalogu pole ZIP64 zawiera dokładnie wartości, które nie mieszczą się
    w 32 bitach; rekord i lokalizator końca katalogu pojawiają się od 65 535 wpisów albo 4 GiB. Nagłówek lokalny
    wpisu strumieniowego powstaje, zanim znany jest rozmiar, więc o jego postaci decyduje `Header.Zip64`
    (rozmiary graniczne, pole z dwoma zerami, deskryptor 64-bitowy). Bez niego wpis, który osiąga 4 GiB − 1
    bajtów, kończy się `write-entry-size` zamiast archiwum, którego część programów nie przeczyta. `Add`
    i `AddRaw` znają rozmiary i wybierają postać same.
11. **Pola stałe.** „Version made by” 4.5 i system 0 (MS-DOS), wersja wymagana według 4.4.3.2, atrybuty zerowe
    poza bitem katalogu, żadnych komentarzy, żadnych pól dodatkowych poza ZIP64. Uprawnień uniksowych nie
    zapisujemy; dojdą jako pole nagłówka, gdy poprosi o nie konsument.
12. **Data wpisu: z nagłówka albo 1980-01-01 00:00:00.** Nieparzysta sekunda i data spoza zakresu formatu to
    `write-timestamp`, nie ciche zaokrąglenie.
13. **Nazwy: te same reguły i kody co w czytniku**, ze wspólnego pakietu `internal/entryname`; konflikty nazw
    (ta sama nazwa, plik w miejscu katalogu) wykrywane przy wywołaniu, przez wyszukiwanie w zbiorach, bez
    iteracji po mapie. Porównania bez względu na wielkość liter pisarz nie robi: to reguła warstwy OPC
    u konsumenta.
14. **`AddRaw` sprawdza to, co kopiuje.** Strumień DEFLATE jest dekodowany w trakcie kopiowania, a rozmiar
    i CRC-32 treści porównywane z opisem; inaczej pisarz mógłby zapisać archiwum, którego czytnik nie przyjmie.
    Koszt to czas dekompresji (kilkaset MB/s), bez dodatkowej pamięci.
15. **Dwa rodzaje błędów.** Wykryty przed pierwszym bajtem wpisu (nazwa, metoda, data, stan) nie zmienia stanu
    pisarza i nie zostawia śladu w archiwum. Wykryty później (błąd celu zapisu, `write-entry-size`,
    `write-raw-content`) jest trwały: archiwum jest niedokończone i każde następne wywołanie zwraca ten sam błąd.
    Błąd `io.Writer` wraca niezmieniony, tak jak błąd `io.ReaderAt` w czytniku.
16. **K8 dla pisarza mierzą wzorce skrótów.** `TestGolden` w obu pakietach na trzech systemach CI z bieżącym
    wydaniem Go oraz zadanie `bytes` na wersji z `go.mod`. Lokalnie sprawdzone 2026-11-27 na go1.26.0, go1.26.6
    i go1.27.0 (darwin/arm64): te same skróty.

## Skutki

- ZW-001…010 przyjęte i pokryte testami; K4 obejmuje teraz 77 wymagań.
- Czarne skrzynki (ZW-007, `tools/compare`, 2026-11-27, macOS): `archive/zip`, 7-Zip, `zipfile` Pythona i bsdtar
  oddają treść wszystkich 7 archiwów pisarza; UnZip w wydaniu Apple 6 z 7 (nie tworzy plików o nazwach spoza
  ASCII, tak samo jak dla archiwów innych programów: `differences.md`).
- Rozmiar (ZW-006, `tools/bench`): 1,020 wyniku `compress/flate` na znacznikach i 1,052 na wierszach liczb
  (próg 1,1).
- Kampania fuzzingu dla wydania musi objąć `FuzzRoundTrip` i `FuzzDeflate`; zlecenie na commicie kandydata.
- Do decyzji właściciela: czy v0.3.0 (pisarz) wychodzi osobno po v0.2.0, czy jedno wydanie obejmuje etapy 3 i 4;
  czy 1.0 obejmuje pisarza (pytanie 10).
