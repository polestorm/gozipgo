# gozipgo — instrukcje dla Claude Code

Projekt: czytnik i pisarz archiwów ZIP w czystym Go, pisany od zera ze specyfikacji formatu, dla wejścia,
któremu nie ufamy. Cele: **własna implementacja kontenera, bez `archive/zip`**; każda struktura archiwum sprawdzona
względem pozostałych (jedna interpretacja albo odrzucenie); limity domyślnie włączone; błędy ze stałym kodem
i miejscem; biblioteka nie dotyka systemu plików ani sieci; wynik powtarzalny co do bajta i niezależny od wersji
kompilatora; zero cgo i zero zależności poza biblioteką standardową; mały kod, który da się przeczytać i zwalidować.
Projekt jest agnostyczny wobec odbiorców.

Zacznij od `README.md`, potem `docs/roadmap.md`, `docs/adr/0001-scope-and-acceptance.md`, `docs/design.md`,
`docs/provenance.md` i `docs/adr/open-questions.md`.

## Po co i w jakim układzie (stan 2026-11-26)

- Rodzina go*go potrzebuje ZIP w kilku miejscach: `../godocxgo` (DOCX to pakiet OPC w archiwum ZIP; **pierwszy
  konsument**, jego etap 1 czeka na `gozipgo` v0.1.0 i `goxmlgo` v0.2.0), przyszły czytnik XLSX, paczki JPK do
  wysyłki (zapis), prywatny `gozipbundlego` (dziś nakładka na `archive/zip`, czyta paczki z zewnątrz), ewentualnie ODT.
- `archive/zip` z biblioteki standardowej nie spełnia naszych wymagań dla wrogiego wejścia (ADR 0001, kontekst):
  nie ma limitów, przepuszcza ścieżki `../` i powtórzone nazwy, ufa katalogowi centralnemu bez porównania
  z nagłówkami lokalnymi, nie wykrywa wpisów nachodzących na siebie, nie ma stałych kodów błędów, a jego zachowanie
  zmienia się z wersją Go. Decyzja właściciela z 2026-11-26: własna implementacja.
- To zmienia sens litery **I** w zasadach ROADIE rodziny (`../polestorm/README.md`): niezależność nie tylko od
  cudzych silników, lecz od źródeł nieznanych, nietestowanych, niezaufanych albo niewalidowalnych. Biblioteka
  standardowa zostaje tam, gdzie jej wynik jest w całości wyznaczony przez specyfikację i sprawdzalny (CRC-32),
  i jako wyrocznia w testach.
- Rodzina: `../goxmlgo`, `../goxsdgo`, `../gomdgo`, `../gomermaidgo`, `../gopdfgo`, `../godocxgo` powstają tak samo
  (od zera ze specyfikacji, czarna skrzynka jako odniesienie, korpus, wymagania, datowane pomiary). **Najbliższe
  wzory to `../goxmlgo` i `../goxsdgo`**: `docs/testing.md`, `requirements/`, `tools/fetch`, `tools/fuzz`,
  `tools/evidence`, `tools/bench`, `tools/compare` jako osobne moduły, test pokrycia wymagań, wzorzec API, pliki
  zapadek, `docs/methodology.md` i `docs/results/`. Narzędzia z tych projektów są nasze: wolno je przenieść.

## Kryteria gotowości (skrót ADR 0001)

| # | Kryterium | Gdzie mierzone | ROADIE |
|---|---|---|---|
| K1 | inwentarz formatu: każdy rekord, pole, bit flag i metoda kompresji z zakresu specyfikacji ma wymaganie: obsługa albo odrzucenie ze stałym kodem; nic nie jest ignorowane po cichu | `TestFormatInventory` | I |
| K2 | korpus zgodności: wszystkie archiwa z prawdziwych programów (Info-ZIP, 7-Zip, Windows, macOS, Python, Java, Go, Word, LibreOffice) czytane w 100%: lista wpisów i SHA-256 treści zgodne z manifestem | `TestInteropCorpus` | R |
| K3 | zgodność z czarnymi skrzynkami: na korpusie zgodności ta sama lista i treść co `archive/zip`, Info-ZIP, 7-Zip i `zipfile` Pythona (100%); każde wrogie archiwum odrzucone z oczekiwanym kodem; każda różnica w `docs/differences.md` | `tools/compare` (osobny moduł) | R |
| K4 | każde wymaganie z `requirements/` ma test; publiczne API zgodne z zapisanym wzorcem | `TestRequirementsCovered`, `TestAPISnapshot` | R |
| K5 | fuzzing ≥ 24 h łącznie bez paniki i bez naruszenia niezmienników (to, co przyjęte, jest spójne; to, co przyjęte ściśle, `archive/zip` czyta tak samo) | `FuzzReader`, `FuzzInflate`, `FuzzRoundTrip`, `FuzzDeflate` | I |
| K6 | bezpieczeństwo: wrogie archiwa (bomby, nakładające się wpisy, kłamiące rozmiary, `../`, powtórzone nazwy, wiele końców katalogu) odrzucone w < 1 s i < 64 MB; biblioteka nie importuje `os` ani `net`; czas liniowy | `TestHostileArchives`, `TestNoIOImports`, `TestPathological` | I |
| K7 | wydajność: rozpakowanie nie wolniejsze niż 1,5× `archive/zip` na tych samych danych; pamięć stała względem rozmiaru treści i zmierzona na wpis; archiwum ZIP64 > 4 GB czytane w < 64 MB | `tools/bench`, `TestLargeArchive` | E |
| K8 | powtarzalność i konsument: te same wyniki w 10 przebiegach i na każdym systemie z CI; zapis daje te same bajty niezależnie od wersji Go; `godocxgo` czyta swój korpus bez zmian w `gozipgo` | `TestDeterministic`, `TestGolden` (pisarz i kompresor, job `bytes` w CI), u konsumenta | D |
| K9 | pakiet dowodów: wymagania → testy → wynik ostatniego przebiegu, tabela K1–K9 ze źródłem każdej liczby | `tools/evidence` | R |

Po każdym etapie dopisz wiersz do tabeli pomiarów w `docs/testing.md` (data, commit, liczby K1–K3) i zaktualizuj
kolumnę „Stan” w `docs/roadmap.md`.

## Jak pracujemy

- etapy w kolejności z `docs/roadmap.md`; **etapy 0 i 1 wykonane 2026-11-26** (narzędzia, budowniczy, korpus,
  inwentarz formatu, czytnik katalogu: ADR 0002), **etap 2 wykonany 2026-11-27** (nagłówki lokalne, układ pliku,
  własny dekoder DEFLATE, `Open`: ADR 0003); **v0.1.0 wydane 2026-11-27**; **etap 3 wykonany 2026-11-27** poza kampanią fuzzingu
  (korpus wrogi, `tools/compare`, `tools/bench`: ADR 0004);
  **etap 4 wykonany 2026-11-27** (`zipwriter`, własny kompresor `internal/deflate`: ADR 0005); **v0.2.0 obejmie etapy 3 i 4**
  (decyzja właściciela z 2026-11-27) i wyjdzie po kampanii fuzzingu 24 h na commicie kandydata (K5); **następny: etap 5** (odbiór: K1–K9, pakiet dowodów, próba u `godocxgo`);
- **v0.1.0 po etapie 2** (czytnik kompletny) odblokowuje `godocxgo`;
- pytania otwarte (`docs/adr/open-questions.md`) rozstrzygamy w etapach, z rekomendacją; pytanie 1 rozstrzygnięte
  2026-11-26 (własna dekompresja DEFLATE), pytanie 2 rozstrzygnięte 2026-11-27 (własny kompresor);
  `compress/flate` tylko w testach i narzędziach pomiaru;
- **bajty pisarza są kontraktem**: każda zmiana w `internal/deflate` albo `zipwriter`, która zmienia wzorce
  `testdata/golden.tsv`, wymaga decyzji, `-update-golden` i wpisu w `CHANGELOG.md`;
- wymagania w `requirements/` są szkicami do czasu rozpoczęcia ich etapu (`requirements/README.md`); etap zaczyna
  się od potwierdzenia swoich wymagań;
- najpierw test (archiwum zbudowane w teście albo plik korpusu), potem kod;
- repozytorium `github.com/polestorm/gozipgo` (publiczne, do założenia; tagi wydań podpisane); commit i push tylko
  na prośbę właściciela.

Zasady:
- **czysty rodowód** (`docs/provenance.md`): piszemy z APPNOTE.TXT (PKWARE), ISO/IEC 21320-1 i RFC 1951. Nie czytaj,
  nie kopiuj i nie przenoś kodu innych implementacji ZIP i DEFLATE: **także `archive/zip` i `compress/flate`
  z biblioteki standardowej Go** (inaczej niż w pozostałych projektach rodziny: tu biblioteka standardowa jest
  konkurentem i wyrocznią, więc nie może być wzorem), Info-ZIP, zlib, miniz, 7-Zip, libzip, libarchive,
  `zipfile` Pythona, `java.util.zip`, klauspost/compress i podobnych. Wolno je uruchamiać jako czarną skrzynkę;
- **zero zależności i zero cgo**: `go.mod` w katalogu głównym nie ma sekcji `require`. Pakiety biblioteki nie
  importują `archive/zip` ani `compress/flate`; z biblioteki standardowej wolno `io`,
  `errors`, `hash/crc32`, `encoding/binary`, `unicode/utf8`, `sort`, `strconv` i podobne pakiety bez wejścia
  i wyjścia. Test importów pilnuje listy;
- **biblioteka nie dotyka systemu plików ani sieci**: wejściem jest `io.ReaderAt` z rozmiarem, wyjściem `io.Reader`
  treści wpisu. Rozpakowywanie na dysk nie należy do biblioteki;
- **jedna interpretacja albo odrzucenie**: gdy dwie struktury archiwum mówią co innego (katalog centralny i nagłówek
  lokalny, rozmiar zadeklarowany i rzeczywisty, dwa końce katalogu), archiwum jest odrzucane ze stałym kodem.
  Każde poluzowanie to nazwana opcja, domyślnie wyłączona, a jej użycie zostawia ślad w wyniku;
- **limity liczone na danych rzeczywistych**, nie na polach nagłówków, które pisze atakujący;
- **powtarzalność**: żadnej iteracji po mapie w drodze do wyniku, żadnego czasu, strefy czasowej ani losowości;
  pisarz daje te same bajty na każdym systemie i w każdej wersji Go;
- **porównania z innymi** (`archive/zip`, Info-ZIP, 7-Zip, Python i każdy inny kandydat): wsad publiczny albo
  generowany deterministycznie, narzędzie pomiaru w repozytorium, metoda w `docs/methodology.md`, surowy wynik
  w `docs/results/`; liczba bez odsyłacza nie trafia do README;
- **fuzzing** (`docs/fuzzing.md`): kampanie na Synology przez wspólną kolejkę, na Macu najwyżej w nocy na zasilaniu;
  lokalnie tylko krótki przebieg sprawdzający poprawkę (≤ 2 min, `-parallel 2`). W poleceniach dla podagentów
  przekazuj tę regułę wprost;
- **żadnych danych prywatnych w repozytorium**: korpus to archiwa z treścią wymyśloną i publiczne zestawy testowe;
- nazwy plików, pakietów i identyfikatorów po angielsku; komentarze dokumentacyjne publicznego API, `README.md`,
  `docs/provenance.md` i `docs/methodology.md` po angielsku; komentarze wewnętrzne, ADR, plan, wymagania i pomiary
  po polsku; komunikaty biblioteki po angielsku, ze stałym kodem do tłumaczenia przez wywołującego;
- każda decyzja = plik w `docs/adr/`; pytania bez decyzji w `docs/adr/open-questions.md`;
- liczby są datowane i podpisane sposobem pomiaru;
- opisy commitów po angielsku; przed commitem muszą przechodzić `gofmt`, `go vet ./...` i `go test ./...`.
