# 0007 Szybszy kompresor i samokontrola pisarza

Status: przyjęta · 2026-11-27 (decyzja właściciela: przepisać kompresor przed pierwszym wydaniem pisarza
i dodać samokontrolę; po pomiarze i znalezisku fuzzingu tego samego dnia: samokontrola domyślnie włączona
z nazwaną opcją wyłączenia, limity czytnika nie są limitami pisarza)

## Kontekst

Pomiar z `klauspost/compress` (commit `252d5e3`) pokazał, że nasz zapis jest od 2,5 do 4,7 raza wolniejszy.
Kompresor z ADR 0005 szukał dopasowań w łańcuchach skrótów, do 32 kandydatów na pozycję, i na każdą pozycję
przechodził przez kilka wywołań funkcji. Profil: 59% czasu w przeszukiwaniu łańcucha, 20% we wpisach do
łańcuchów, 8% w przesuwaniu bufora.

Pisarz nie ma jeszcze wydania ani piszącego konsumenta, więc bajty wyjścia można zmienić bez kosztu dla nikogo.
Właściciel zdecydował 2026-11-27 zrobić to teraz, w trzech krokach: tablice bez łańcuchów i większy bufor,
przepisana pętla główna, samokontrola pisarza. Zastępuje to pkt 1, 6 i 7 ADR 0005 oraz liczbę z jego pkt 2;
pozostałe punkty (bajty jako kontrakt, bloki, kody Huffmana, pełne kody) obowiązują bez zmian.

## Decyzje

### Kompresor (`internal/deflate`)

1. **Dwie tablice skrótów zamiast łańcuchów.** Krótka (2^15 pozycji) pamięta ostatnią pozycję o tych samych
   czterech bajtach, długa (2^17) o tych samych ośmiu. Pozycja ma więc najwyżej dwóch kandydatów: długi zwykle
   zgadza się dalej, krótki jest bliżej. Reguły wyboru są stałe:
   - najpierw kandydat długi; krótkiego sprawdzamy tylko wtedy, gdy długi dał mniej niż 8 bajtów;
   - przy równej długości wygrywa bliższy, bo jego odległość ma krótszy zapis;
   - najkrótsze dopasowanie ma cztery bajty; takie, które leży dalej niż 2048 bajtów, jest pomijane, bo kosztuje
     więcej niż cztery literały;
   - z pozycji objętych dopasowaniem do obu tablic trafiają 64 ostatnie (wszystkie: 0,2% mniej na tekście, ale
     dane z długimi powtórzeniami dwa razy wolniej).
   Nadal jeden algorytm, stałe parametry, żadnych poziomów.
2. **Odroczony wybór zostaje** (RFC 1951, rozdz. 4), z progiem 32 zamiast 64: dopasowanie krótsze niż 32 bajty
   czeka jedną pozycję i ustępuje dłuższemu. Na danych bez dopasowań przeskok bez szukania działa jak dotąd
   (o jedną pozycję więcej po każdych 32 pudłach), ale najwyżej 32 pozycje naraz zamiast 8: dane, których nie da
   się skompresować, idą o 13% szybciej, a tekst przeplatany takimi danymi rośnie o 0,1%.
3. **Dopasowanie jest rozszerzane wstecz.** Tablice pamiętają tylko ostatnią pozycję, więc początek powtórzenia
   bywa znaleziony kilka bajtów za późno. Literały stojące tuż przed dopasowaniem w tym samym bloku, które
   zgadzają się z bajtami przed kandydatem, są cofane i wchodzą do dopasowania. Zysk na prawdziwych plikach:
   około 1% rozmiaru bez kosztu czasu.
4. **Jedna pozycja na obrót pętli, stan w zmiennych lokalnych.** Osiem bajtów wczytanych raz daje oba skróty
   i porównanie z kandydatami; na pozycję nie ma wywołań funkcji. Częstości symboli są liczone na bieżąco,
   a bity bloku zapisywane po osiem bajtów naraz. Blok kończy się tylko w chwili, w której żadne dopasowanie nie
   jest odroczone, więc granica bloku jest zawsze bieżącą pozycją.
5. **Bufor wejścia 1 MiB zamiast 128 KiB, a `Reset` nie czyści tablic.** Większy bufor oznacza, że dane
   i tablice przesuwa się raz na około 950 KiB wejścia, nie raz na 64 KiB. Nowy strumień zaczyna się w buforze za
   danymi poprzedniego, a pozycja sprzed jego początku nie jest kandydatem, tak samo jak brak pozycji. Dzięki
   temu mały wpis nie płaci za czyszczenie 640 KiB tablic. Rozmiar bufora i miejsce strumienia w buforze nie
   wpływają na bajty wyjścia (`TestBufferPositionIndependence`, `TestFarthestMatchAcrossMove`, `FuzzDeflate`).
   Koder zajmuje około 1,8 MiB (dotąd 0,5 MiB).
6. **Bajty wyjścia zmieniły się raz, przy tej przebudowie.** Wzorce w obu `testdata/golden.tsv` zostały
   zastąpione; żadne wydanie nie zawierało poprzednich. Kontrakt z ADR 0005 pkt 2 obowiązuje dalej, z jedną
   liczbą zmienioną: pozycja jest kodowana, gdy ma przed sobą 266 bajtów (najdłuższe dopasowanie i osiem bajtów
   skrótu) albo strumień się kończy. Te same skróty na go1.26.0, go1.26.6 i go1.27.0 (darwin/arm64, 2026-11-27).

### Pisarz (`zipwriter`)

7. **Pisarz dekoduje każdy własny strumień DEFLATE i porównuje wynik z treścią wpisu (ZW-011).** Rozmiar
   i CRC-32 tego, co wychodzi z naszego dekodera, muszą być równe rozmiarowi i CRC-32 treści podanej przez
   wywołującego, a strumień musi kończyć się ze swoim ostatnim bajtem. Inaczej: `write-self-check`.
   - Po co: błąd kompresora nie jest odrzuceniem, tylko archiwum, które rozpakowuje się do czegoś innego. Testy
     i fuzzing zmniejszają to ryzyko, samokontrola je zamyka po stronie zapisu: złe dane nie wychodzą bez błędu.
   - `Add` sprawdza przed zapisem czegokolwiek, więc po błędzie archiwum da się dokończyć bez tego wpisu. Wpis
     z `Create` jest w chwili sprawdzenia już zapisany, więc błąd jest trwały.
   - Dekoder ciągnie wejście sam i nie umie czekać na jego dalszy ciąg. Dla wpisu strumieniowego bajty
     kompresora czekają więc w kolejce, a dekoder pracuje tylko wtedy, gdy czeka ich co najmniej 128 KiB: jedno
     jego wywołanie dekoduje najwyżej okno (32 768 bajtów treści) i zużywa przy tym mniej niż 75 000 bajtów
     strumienia (rachunek przy stałej `checkReserve`). Gdyby rachunek zawiódł, kontrola kończy się błędem,
     nie milczeniem. Pamięć: do około 200 KiB kolejki i dekoder (około 50 KiB) na pisarza.
   - Kontrola kosztuje około połowy szybkości zapisu wpisów kompresowanych (nasz dekoder jest tylko około
     1,7 raza szybszy od kompresora; pomiar w pkt 9). Decyzja właściciela z 2026-11-27: **domyślnie włączona,
     wyłączana nazwaną opcją** `Options.SkipSelfCheck`, podawaną przez `NewWithOptions`; `New` zostaje i znaczy
     opcje domyślne. Opcja jest dla wywołującego, który sprawdza archiwum inaczej, na przykład czytając je po
     zapisie. Nie zmienia bajtów archiwum (`TestSkipSelfCheck`, `FuzzRoundTrip`), więc inaczej niż poluzowania
     czytnika nie zostawia śladu w wyniku: nie ma czego oznaczać.
8. **Limity czytnika nie są limitami pisarza.** `FuzzRoundTrip` pokazał 2026-11-27, że pisarz zapisuje nazwę
   dłuższą niż 4096 bajtów (format pozwala na 65 535), a czytnik z domyślnymi opcjami odrzuca takie archiwum
   (`limit-name-length`). To samo dotyczy pozostałych limitów: ponad 65 536 wpisów, katalog ponad 64 MiB, wpis
   ponad 1 GiB, łącznie ponad 4 GiB czytanej treści. Limity bronią czytającego przed wrogim wejściem i ustawia
   je czytający; pisarz zapisuje dane, którym wywołujący ufa, i ma zapisać wszystko, co format mieści (po to
   jest ZIP64). Decyzja właściciela: pisarz zostaje bez zmian, ZW-007 mówi „o ile archiwum mieści się
   w limitach czytnika”, dokumentacja pakietu wymienia limity domyślne, a testy czytają z limitem nazwy
   podniesionym do granicy formatu. Odrzucone: limit 4096 bajtów nazwy w pisarzu (zamyka jeden z pięciu
   limitów) i własne limity pisarza równe domyślnym czytnika (pisarz odmawiałby wpisu ponad 1 GiB).

### Pomiar

9. Wynik `tools/bench` po tej zmianie jest w `docs/comparison.md` i `docs/results/`; README bierze liczby stamtąd.
   Commit `1d5412e`, Apple M4, mediana z 7 przebiegów, zapis z samokontrolą / bez niej / `klauspost/compress`:
   znaczniki 180 / 272 / 335 MB/s (poprzedni kompresor: 72), wiersze liczb 85 / 116 / 140 (55), szum
   429 / 478 / 6893 (208), zera 1262 / 1936 / 4774 (1153), 100 000 małych wpisów 107 / 158 / 301 (89). Rozmiar
   wobec `compress/flate` na poziomie domyślnym: znaczniki 1,069 (było 1,078), wiersze liczb 1,010 (1,045),
   małe wpisy 1,019 (0,998). Zostaje do zrobienia, jeśli będzie potrzeba: rozpoznawanie danych, których nie
   da się skompresować (tu `klauspost/compress` jest 16 razy szybszy), i szybka ścieżka dekodera, która
   obniżyłaby koszt samokontroli.

## Sprawdzone i odrzucone

- Wpis do tablic co drugiej pozycji objętej dopasowaniem: 5% szybciej, archiwum o 3% większe.
- Trzeci kandydat w odległości ostatniego dopasowania: bez zysku na rozmiarze, 3–5% wolniej.
- Próg odroczenia 8: na danych generowanych szybciej i mniej, na prawdziwych plikach archiwum o 1–2% większe.
  Parametry dobieraliśmy na obu rodzajach danych właśnie dlatego, że generowane dają się łatwo przestroić.
- Większe tablice (2^16 i 2^18): 0,2% mniej, bez zysku czasu.
- Szybka ścieżka dekodera (kilka symboli naraz) nie weszła do tej zmiany: dekoder czyta wrogie dane, więc
  wymaga osobnej decyzji i osobnej kampanii fuzzingu.

## Skutki

- ZW-011 przyjęte i pokryte testami; nowy kod błędu `write-self-check`; K4 obejmuje 86 wymagań.
- Testy kompresora sprawdzono celowo wstrzykniętymi usterkami (przeliczanie tablic przy przesunięciu, granica
  okna, początek strumienia, zapas symboli w bloku, koniec strumienia z odroczonym dopasowaniem): każda jest
  wykrywana. Dwa testy doszły po tej próbie.
- Kampania fuzzingu biegnąca na commicie `1469779` obejmuje poprzedni kompresor. Jej wynik dla czytnika
  i dekodera pozostaje ważny, ale K5 dla wydania wymaga nowej kampanii na commicie z tą zmianą.
- Gdyby rozmiar stał się ważniejszy niż szybkość, droga z ADR 0005 pkt 7 pozostaje: nazwany poziom jako pole
  `Header`, z własnym wzorcem skrótów. Poprzedni algorytm (łańcuchy) jest w historii repozytorium.
