# 0002 Reguły czytania katalogu (etap 1)

Status: przyjęta · 2026-11-26 (potwierdzona przez właściciela 2026-11-27)

## Kontekst

Etap 1 zaczyna się od przeglądu szkiców wymagań `DIR`, `NAME`, `FMT` z tekstem APPNOTE 6.3.10 w ręku
(`requirements/README.md`) i rozstrzyga pytania otwarte, które blokują czytanie katalogu: 4, 5, 8, częściowo 9 i 11.
Rekomendacje z `open-questions.md` zostały przyjęte, poza miejscami opisanymi niżej, gdzie tekst specyfikacji albo
korpus zgodności pokazały coś innego niż szkic.

## Decyzje

1. **Cechy spoza zakresu dotyczące jednego wpisu nie odrzucają archiwum.** Szyfrowanie (bity 0 i 6, pola 0x0017
   i 0x9901), dane łatek (bit 5, pole 0x000f), metoda inna niż 0 i 8 oraz wersja wymagana powyżej 4.5 dają wpis na
   liście z `Entry.Unsupported` równym kodowi, który zwróci `Open` (FMT-004, ENT-011). Kolejność, gdy pasuje kilka:
   szyfrowanie, łatki, metoda, wersja. Cechy, przy których nie można ufać samej liście, odrzucają archiwum: bit 13
   (szyfrowany katalog), rekord danych dodatkowych archiwum, podpis cyfrowy katalogu, rekord ZIP64 w wersji 2 albo
   z sektorem danych, pola wieloczęściowe, bity flag zarezerwowane i nieużywane.
2. **Bity zarezerwowane i nieużywane flagi (4, 7–10, 12, 14, 15) odrzucają archiwum** kodem `unsupported-flag`.
   Nie znamy ich znaczenia, więc nie wiemy, czy inny program przeczyta wpis tak samo. W korpusie zgodności żaden
   program ich nie ustawia.
3. **Koniec katalogu (DIR-002).** Kandydatem jest każda sygnatura w oknie 22 + 65 535 bajtów, po której rekord
   z komentarzem kończy się dokładnie na końcu pliku. Dokładnie jeden kandydat: przyjęcie; dwa:
   `zip-multiple-directory-ends`; żaden: `zip-trailing-data`, gdy jakiś rekord kończy się przed końcem pliku,
   inaczej `zip-no-directory-end`. Sygnatura w danych (archiwum Stored w archiwum), która warunku nie spełnia,
   nie jest drugą interpretacją.
   **Zmiana z 2026-11-27 (poprawka bezpieczeństwa):** pierwotnie to samo dotyczyło sygnatury w komentarzu.
   `FuzzReader` znalazł archiwum przyjęte ściśle, które `archive/zip` odrzuca, a zbudowany z niego przykład
   (drugie archiwum i jeden bajt w komentarzu pierwszego) pokazał, że `archive/zip`, `zipfile` Pythona, UnZip
   i bsdtar biorą ostatnią sygnaturę bez sprawdzania długości komentarza i czytają inny plik niż my. Teraz
   sygnatura końca katalogu gdziekolwiek po pierwszym bajcie wybranego rekordu (w jego polach albo w komentarzu)
   daje `zip-multiple-directory-ends`. Koszt: archiwum, którego komentarz zawiera cztery bajty `PK\x05\x06`,
   jest odrzucane; w korpusach takiego nie ma.
4. **Lokalizator ZIP64 jest rozpoznawany po sygnaturze**, także gdy zwykły rekord nie ma wartości granicznych:
   wtedy rekordy ZIP64 muszą być w pełni zgodne ze zwykłym rekordem, inaczej `zip64-inconsistent`. Powód: program,
   który woli rekordy ZIP64, przeczytałby inny katalog. Rekordy ZIP64 „na zapas” (Info-ZIP `-fz`) są przyjmowane.
   Lokalizator musi mieć liczbę dysków 1, rekord ZIP64 musi kończyć się dokładnie na początku lokalizatora.
5. **Pole dodatkowe ZIP64 w katalogu (DIR-008)** musi zawierać dokładnie pola, do których odsyłają wartości
   graniczne nagłówka (APPNOTE 4.5.3: „MUST only appear if…”). Korpus (Info-ZIP `-fz`, Python `force_zip64`,
   7-Zip, Go) tę regułę spełnia. **Poprawka po zestawach pobranych (2026-11-26):** archiwum
   `test/success/zip64.zip` z zestawu yauzl ma pole w postaci pełnej (oba rozmiary i przesunięcie, choć nagłówek
   odsyła tylko do rozmiarów). Postać pełna jest przyjmowana, gdy czytanie według wartości granicznych i czytanie
   pozycyjne dają te same wartości, a pola nadmiarowe równają się nagłówkowi: wtedy żaden program nie przeczyta
   wpisu inaczej. Pole ZIP64, do którego nie odsyła żadne pole nagłówka, nadal odrzuca archiwum.
6. **Nazwy bez bitu 11 (pytanie 4).** Domyślnie tylko ASCII 0x20–0x7E. Zamiast planowanej opcji `AllowCP437`
   jest jedna opcja `Options.UnflaggedNames` o wartościach `ASCIIOnly` (domyślna), `CP437`, `UTF8` i `CodePage`: dwie osobne
   opcje wymagałyby reguły pierwszeństwa, czyli zgadywania. **Znalezisko korpusu:** `zip` i `ditto` z macOS
   zapisują nazwy w UTF-8 bez bitu 11 i bez pola Unicode Path; to 9 z 22 archiwów korpusu (każde zawiera plik
   z polskimi literami). W trybie ścisłym są odrzucane kodem `name-encoding`, z `UnflaggedNames: UTF8` czytane
   zgodnie z manifestem (`differences.md`). **Decyzja właściciela z 2026-11-27: domyślne zostaje `ASCIIOnly`.** Pytanie brzmiało: czy domyślne ma zostać `ASCIIOnly`
   (rekomendacja: tak, bo nazwa bez bitu 11 ma dwa odczytania i właśnie tak przemyca się różne nazwy do różnych
   programów; konsument, który przyjmuje archiwa z macOS, włącza `UTF8` świadomie i widzi to w `Relaxed()`).
   **Drugie znalezisko (2026-11-26):** 7-Zip na polskim Windows 11 zapisuje nazwy w stronie OEM systemu (CP852,
   nie CP437 i nie cp1250) bez bitu 11; ten sam folder spakowany Eksploratorem ma UTF-8 z bitem 11. Stąd wartość
   `CodePage`: tablicę 128 znaków podaje wywołujący w `Options.CodePage`, biblioteka nie zawiera stron kodowych
   poza CP437 z APPNOTE. Tablica nie może zamienić bajtu na znak poniżej U+0080 (ukośnik, kropka), bo nazwa
   miałaby wtedy odczytanie inne niż jej bajty; taki bajt, tak jak brak tablicy, daje `name-encoding`.
   Do etapu 2: 7-Zip zapisuje Unicode Path tylko w nagłówku lokalnym, więc porównanie nagłówka lokalnego
   z katalogiem musi dopuścić to pole po stronie lokalnej i sprawdzić je względem nazwy zdekodowanej.
7. **Znaki sterujące w nazwie** (poniżej U+0020 i U+007F) są odrzucane kodem `name-control-character` w obu
   kodowaniach. Szkic NAME-002 mówił to tylko o nazwach ASCII; dla UTF-8 reguła jest ta sama, żeby nazwa nie
   zależała od bitu 11.
8. **Unicode Path (0x7075, pytanie 4):** jeśli jest, musi mieć wersję 1, CRC-32 pola nazwy i dawać dokładnie nazwę
   wpisu po zdekodowaniu; inaczej `name-unicode-path`. APPNOTE 4.6.9 każe przy złym CRC pole ignorować; nie
   ignorujemy, bo program, który CRC nie sprawdza, wziąłby drugą nazwę. Unicode Comment (0x6375) i pozostałe pola
   są wydawane jako surowe bajty: komentarzy nie interpretujemy.
9. **Komentarze** są wydawane tak, jak zapisano. Jedyna reguła: komentarz wpisu z bitem 11 musi być poprawnym
   UTF-8 (APPNOTE 4.4.4, „MUST”), inaczej `zip-comment-encoding`.
10. **Powtórzenia nazw (pytania 5 i 8):** kolizją są dwie takie same nazwy, plik `a` i katalog `a/` oraz plik `a`
    i dowolny wpis `a/…`. `Options.FoldCase` rozszerza porównanie na wielkość liter ASCII; domyślnie wyłączona.
    Zgłaszana para jest wyznaczona treścią archiwum (porządek sortowania), nie musi być pierwszą w kolejności
    katalogu.
11. **Wpisy katalogów (pytanie 8):** nazwa zakończona `/` wymaga obu rozmiarów równych zero (APPNOTE 4.3.8),
    inaczej `zip-directory-entry-size`. Dla plików o rozmiarze zero ta sama reguła 4.3.8 **nie** jest egzekwowana:
    Python i Go zapisują pusty plik metodą Deflate jako strumień 2 albo 5 bajtów (`differences.md`); treść
    sprawdzi `Open`.
12. **Limity etapu 1 (pytanie 9, część):** 65 536 wpisów, katalog 64 MiB, nazwa 4 096 bajtów; komentarz i pola
    dodatkowe mają limity z wartością domyślną równą maksimum formatu (65 535), czyli do zaciśnięcia przez
    wywołującego. Wartości potwierdza pomiar w etapie 2 razem z limitami treści.
13. **Najniższa wersja Go (pytanie 11):** bieżące wydanie, dziś 1.26 (`go.mod`), a CI testuje `stable`. Decyzja
    właściciela z 2026-11-26: nie wymagamy ani nie testujemy starszych wersji.
14. **Kod biblioteki nie importuje `fmt`** (wciąga `os`): lista dozwolonych importów jest w `api_test.go`
    i pilnuje jej `TestNoIOImports`.
15. **Błędy źródła:** błąd `io.ReaderAt` wraca bez zmiany; źródło krótsze niż podany rozmiar daje
    `io.ErrUnexpectedEOF`, źródło bez postępu i bez błędu `io.ErrNoProgress`.

## Czego etap 1 jeszcze nie sprawdza

Nagłówków lokalnych, deskryptorów, układu pliku (nachodzenie, luki, dane przed pierwszym wpisem) ani treści.
Archiwum przyjęte przez `New` w tej wersji ma spójny katalog, ale nie jest jeszcze sprawdzone względem danych:
do etapu 2 pakiet nie nadaje się do użycia na wrogim wejściu i dokumentacja pakietu to mówi. Przesunięcie nagłówka
lokalnego i rozmiary z katalogu są wydawane bez porównania z plikiem. Dane przed archiwum dają dziś
`zip-directory-out-of-bounds`; opcje `AllowPrefix` i `AllowGaps` (pytanie 3) należą do etapu 2.

## Konsekwencje

+ każdy wiersz inwentarza formatu dotyczący katalogu ma werdykt sprawdzony archiwum (K1 dla katalogu);
+ kolejność zgłaszania wad jest zapisana w `docs/errors.md` i ma test (DET-005);
− archiwa z macOS z nazwami spoza ASCII wymagają jawnej opcji;
− reguły 4, 5 i 8 są ostrzejsze niż praktyka części programów; każde znalezisko z nowych archiwów korpusu trafia
  do `differences.md`.
