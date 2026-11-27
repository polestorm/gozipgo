# 0006 Dwa poluzowania po próbie na korpusie pierwszego konsumenta

Status: przyjęta · 2026-11-27 (potwierdzona przez właściciela 2026-11-27)

## Kontekst

Po wydaniu v0.1.1 przeczytaliśmy czytnikiem w trybie ścisłym cały korpus `godocxgo` (3126 dokumentów): 3094
przeczytane w całości, 26 odrzuconych także przez `archive/zip` (pliki zaszyfrowane i uszkodzone próbki z fuzzerów),
6 odrzuconych tylko przez nas. Z tych sześciu dwa są uszkodzone celowo. Cztery to dokumenty z danych testowych
LibreOffice, które wszystkie zmierzone programy (`archive/zip`, UnZip, 7-Zip, `zipfile` Pythona, bsdtar z pliku
i ze strumienia) czytają z tą samą treścią:

- `symbol_chicago_list.docx`, `tdf101627.docx`: „version needed to extract” równe 788, czyli 2.0 w młodszym bajcie
  i 3 (Unix) w starszym. Dwa archiwa z zestawów pobranych tego repozytorium mają to samo
  (`libzip-regress/…/test_open_multiple.zip`, `rc-zip/testdata/meta.zip`);
- `fdo70812.docx`, `tdf128245.docx`: nagłówek lokalny z prawdziwymi rozmiarami i polem ZIP64 z samych zer.

## Decyzje

1. **Tryb ścisły się nie zmienia.** Pozostaje domyślny i odpowiada temu, co mówi litera specyfikacji i nasze
   reguły jednego odczytania; walidacja i porównania robione w trybie domyślnym nie zależą od tej decyzji.
2. **`Options.AllowVersionHostByte`**: dla wpisu liczy się tylko młodszy bajt wersji wymaganej. Pole nie mówi,
   gdzie leżą dane ani ile ich jest; cechy, które naprawdę wymagają nowszej wersji (szyfrowanie, metody
   kompresji), rozpoznajemy po flagach i metodzie. Nie znamy drugiego odczytania treści.
3. **`Options.AllowUnusedZip64`**: pole ZIP64 nagłówka lokalnego z samych zer obok rozmiarów bez wartości
   granicznej jest pomijane, obowiązują rozmiary nagłówka (APPNOTE 4.5.3 każe czytać pole tylko przy wartości
   granicznej). Pole o wartościach innych niż zero i innych niż nagłówek jest odrzucane także z opcją. Ryzyko,
   dla którego to opcja, a nie reguła: program, który ufa polu zawsze, gdy je widzi, przeczytałby pusty plik.
   Wśród zmierzonych takiego nie ma.
4. **Każde zadziałanie zostawia ślad** w `Reader.Relaxed()` (opcja i indeks wpisu), jak pozostałe poluzowania.
5. **Dwie nazwane opcje zamiast jednego przełącznika „relaxed”.** Wywołujący widzi, co dokładnie dopuścił,
   a nowe poluzowanie w przyszłej wersji nie zmieni po cichu zachowania jego programu. Kto chce czytać „tyle co
   inne programy”, włącza obie razem z `UnflaggedNames` i `AllowPrefix`; tak robi kolumna poluzowana w teście
   zestawów pobranych i tak może liczyć wyniki `godocxgo`.

## Skutki

- Korpus `godocxgo`: w trybie ścisłym 3094 z 3126, z obiema opcjami 3098; pozostałe 28 to pliki uszkodzone
  albo niebędące archiwami.
- W tabelach wyników (README, `comparison.md`) wynik trybu ścisłego jest wynikiem głównym; wynik z opcjami
  podajemy obok z przypisem, że to odczyt poluzowany.
- Opcje są nowym API, więc wchodzą do v0.2.0; gałąź `release-0.1` ich nie dostaje, o ile właściciel nie zdecyduje
  inaczej.
