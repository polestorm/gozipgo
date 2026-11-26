# Wymagania: pisarz

Status: szkic

Etap 4.

| Id | Wymaganie |
|---|---|
| ZW-001 | Pisarz zapisuje do `io.Writer` bez cofania się; wpisy są w archiwum i w katalogu w kolejności wywołań. |
| ZW-002 | Nazwy podlegają tym samym regułom co w czytniku (NAME-001…005); nazwa niedozwolona albo powtórzona jest błędem przy `Create`. Nazwy spoza ASCII są zapisywane w UTF-8 z bitem 11. |
| ZW-003 | Pisarz nie czyta zegara: data i czas wpisu pochodzą z nagłówka podanego przez wywołującego, domyślnie 1980-01-01 00:00:00. Pola „version made by”, atrybuty i pozostałe pola stałe mają wartości ustalone w kodzie i opisane w dokumentacji. |
| ZW-004 | Pisarz nie dodaje pól dodatkowych poza ZIP64; ZIP64 pojawia się wtedy i tylko wtedy, gdy rozmiar, przesunięcie albo liczba wpisów tego wymaga. |
| ZW-005 | Metody: Stored i Deflate. Kompresor ma ustalony algorytm i parametry: te same dane dają te same bajty na każdym systemie, architekturze i w każdej wersji Go z CI (pytanie 2). |
| ZW-006 | Każdy strumień kompresora jest poprawnym strumieniem DEFLATE: własna dekompresja i `compress/flate` oddają dane wejściowe. Rozmiar wyniku na korpusie XML nie przekracza 1,1× wyniku `compress/flate` na poziomie domyślnym (cel roboczy). |
| ZW-007 | To, co pisarz zapisał bez błędu, czytnik przyjmuje w trybie ścisłym z tymi samymi nazwami i treścią; Info-ZIP, 7-Zip, Python i `archive/zip` rozpakowują to samo (`tools/compare`). |
| ZW-008 | Ten sam ciąg wywołań daje te same bajty archiwum, niezależnie od podziału danych wpisu na wywołania `Write`. |
| ZW-009 | `Close` zapisuje katalog i koniec katalogu; wpis niezamknięty, zapis po `Close` i błąd docelowego `io.Writer` są błędami ze stałym kodem; po błędzie zapisu pisarz zwraca stale ten sam błąd. |
| ZW-010 | Kopiowanie wpisu z czytnika bez ponownej kompresji (`OpenRaw` → pisarz) daje wpis o tej samej treści i tym samym CRC. |
