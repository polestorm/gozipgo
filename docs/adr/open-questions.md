# Pytania otwarte

Kolejność według etapu, w którym pytanie blokuje pracę. Rozstrzygnięte w etapie 1 (ADR 0002, do potwierdzenia przez
właściciela): 4, 5, 8, 11 i część 9 dotycząca katalogu. Rozstrzygnięte w etapie 2: 1 (decyzja właściciela
z 2026-11-26: dekompresja własna), 3, 9 i 12 (ADR 0003 pkt 6–9, do potwierdzenia przez właściciela). **Nowe z etapu 1:** czy domyślne `UnflaggedNames` ma zostać
`ASCIIOnly`, skoro `zip` i `ditto` z macOS zapisują nazwy UTF-8 bez bitu 11 (ADR 0002 pkt 6; rekomendacja: tak).

1. **Dekompresja DEFLATE: własna czy `compress/flate` (przed etapem 2).** Właściciel zdecydował o własnym
   kontenerze bez `archive/zip`; dekompresja to osobny pakiet biblioteki standardowej. Za własną: stałe kody
   błędów strumienia, dokładna kontrola końca strumienia i liczby zużytych bajtów, zachowanie niezależne od wersji
   Go, spójność z własnym kompresorem pisarza, ok. 500 linii z RFC 1951 i `compress/flate` jako gotowa wyrocznia
   do testów różnicowych. Przeciw: `compress/flate` jest dojrzały i szybki, a wynik poprawnego strumienia jest
   wyznaczony przez specyfikację. Rekomendacja: własna, z granicą wydajności z K7; gdyby K7 nie dało się spełnić,
   wracamy do pytania z pomiarem.
2. **Kompresja w pisarzu (przed etapem 4).** Bajty z `compress/flate` mogą się zmienić z wersją Go, co łamie
   zasadę D („te same bajty w obrębie wersji biblioteki”). Rekomendacja: własny kompresor o ustalonym algorytmie
   (dopasowania przez łańcuchy skrótów o stałych parametrach, kody dynamiczne), jeden poziom kompresji w 1.0.
   Cel: rozmiar nie gorszy niż 1,1× `compress/flate` na poziomie domyślnym na korpusie XML; mierzone w `tools/bench`.
3. **Domyślna ścisłość wobec danych przed archiwum i luk.** Archiwa samorozpakowujące mają program przed
   pierwszym wpisem; niektóre narzędzia zostawiają luki po usuniętych wpisach. Rekomendacja: domyślnie odrzucać
   (to te same miejsca, w których przemyca się treść), opcje `AllowPrefix` i `AllowGaps`, każda widoczna w wyniku.
   Korpus zgodności pokaże, czy któryś z popularnych programów tworzy takie archiwa bez wiedzy użytkownika.
4. **Nazwy bez bitu UTF-8.** APPNOTE mówi wtedy o CP437, a praktyka bywa inna (strona kodowa systemu). Rekomendacja:
   bez bitu 11 przyjmować tylko ASCII; opcja `AllowCP437` dekoduje według tablicy z danych Unicode; innych stron
   kodowych nie zgadujemy. Pole dodatkowe Unicode Path (0x7075) sprawdzamy na zgodność z nazwą albo ignorujemy
   (do decyzji w etapie 1; rekomendacja: gdy jest, musi dawać tę samą nazwę co pole nazwy, inaczej odrzucenie).
   Pomiar z 2026-11-26: archiwum z Eksploratora Windows 11 w polskiej wersji (`windows11-explorer.zip`
   w korpusie zgodności) ma polskie nazwy w UTF-8 z bitem 11, więc domyślne `ASCIIOnly`
   je przyjmuje; cp1250 ani cp852 w nim nie ma. Ten sam folder spakowany
   7-Zipem na tym samym Windows (`windows11-7zip.zip`) ma nazwy w CP852 bez bitu 11, stąd `UnflaggedNames: CodePage`
   z tablicą podawaną przez wywołującego (ADR 0002 pkt 6).
5. **Wykrywanie powtórzonych nazw: dokładne czy bez względu na wielkość liter.** OPC wymaga równoważności bez
   względu na wielkość liter ASCII, systemy plików Windows i macOS też ją stosują. Rekomendacja: `gozipgo`
   odrzuca powtórzenia dokładne; porównanie bez względu na wielkość liter jako opcja (`FoldCase`), domyślnie
   wyłączona; warstwa OPC u konsumenta ją włącza.
6. **Rozpakowywanie na dysk.** Biblioteka nie importuje `os`. Czy rodzina potrzebuje wspólnej, bezpiecznej funkcji
   „rozpakuj do katalogu” (dziś `gozipbundlego.Extract`)? Rekomendacja: nie w `gozipgo` 1.0; jeśli powstanie, to
   jako osobny pakiet z własnymi wymaganiami (dowiązania symboliczne, uprawnienia, nazwy zastrzeżone w Windows).
7. **Adapter `fs.FS`.** `io/fs` nie daje dostępu do dysku, więc adapter nie łamie zasad. Rekomendacja: dodać, gdy
   poprosi o niego konsument; nie w v0.1.0.
8. **Wpisy katalogów i wpisy o zerowej długości.** Wpis z nazwą zakończoną `/` musi mieć rozmiar zero
   (rekomendacja: inaczej odrzucenie). Czy plik `a` i katalog `a/` w jednym archiwum to konflikt? Rekomendacja:
   tak, kod `name-duplicate`, bo na dysku nie mogą współistnieć.
9. **Wartości domyślne limitów.** Propozycja do potwierdzenia pomiarem na korpusie w etapie 2: 65 536 wpisów,
   katalog centralny 64 MiB, nazwa 4 096 bajtów, wpis po rozpakowaniu 1 GiB, łącznie 4 GiB, stosunek kompresji
   wpisu 1 000:1 liczony dopiero powyżej 1 MiB wyjścia (DEFLATE nie przekracza ok. 1 032:1, więc limit łapie
   bomby warstwowe i kłamiące nagłówki; arkusze z powtarzalną treścią miewają ponad 100:1). Konsumenci OPC
   ustawią ciaśniej.
10. **Czy pisarz wchodzi do 1.0.** Patrz `../roadmap.md`: rekomendacja to 1.0 dla czytnika po etapach 0–3 i 5,
    pisarz w 1.1, o ile wcześniej nie pojawi się piszący konsument.
11. **Najniższa wersja Go.** Rozstrzygnięte 2026-11-26 (właściciel): bieżące wydanie, bez wspierania starszych
    (ADR 0002 pkt 13).
12. **Odczyt równoległy.** `io.ReaderAt` pozwala otwierać wiele wpisów naraz. Rekomendacja: `Open` bezpieczne
    przy równoległym użyciu jednego czytnika (limit łączny liczony atomowo), z testem pod `-race`.
