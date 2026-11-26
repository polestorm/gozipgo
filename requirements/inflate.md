# Wymagania: dekompresja DEFLATE

Status: szkic

Zależny od pytania 1: przy decyzji „`compress/flate`” zostają INF-001, INF-006…008 jako wymagania wobec opakowania.

Źródło: RFC 1951. Numery rozdziałów uzupełnia przegląd na początku etapu 2.

| Id | Wymaganie |
|---|---|
| INF-001 | Dekoder daje dla każdego poprawnego strumienia DEFLATE dokładnie te bajty, które wyznacza RFC 1951: bloki niezakodowane (BTYPE 00), ze stałymi kodami Huffmana (01) i z dynamicznymi (10), w dowolnej kolejności, z odwołaniami do okna 32 KiB także przez granice bloków. |
| INF-002 | BTYPE 11 daje `inflate-block-type`. |
| INF-003 | Blok niezakodowany: LEN i NLEN muszą być dopełnieniami (`inflate-stored-length`); bity dopełniające do granicy bajtu są pomijane. |
| INF-004 | Kody dynamiczne: HLIT, HDIST, HCLEN w zakresach z RFC; długości kodów muszą tworzyć zestaw kompletny (ani nadmiarowy, ani niepełny), z wyjątkami, które RFC dopuszcza (jeden kod odległości); powtórzenie długości (symbole 16–18) nie może wyjść poza tablicę ani zaczynać się bez poprzednika; brak kodu końca bloku (256) jest błędem. Naruszenia dają `inflate-code-lengths`. Dokładną listę przypadków granicznych ustala etap 2 z tekstu RFC i zapisuje w `docs/differences.md` tam, gdzie `compress/flate` orzeka inaczej. |
| INF-005 | Symbole długości 286–287 i odległości 30–31 oraz odległość większa niż liczba dotąd wydanych bajtów dają `inflate-distance` albo `inflate-code-lengths`. |
| INF-006 | Koniec wejścia przed końcem ostatniego bloku daje `inflate-truncated`. Dekoder podaje liczbę zużytych bajtów wejścia; bajty wejścia po końcu ostatniego bloku wykrywa wywołujący (`inflate-trailing-data`, ENT-007). |
| INF-007 | Pamięć dekodera jest stała (okno, tablice kodów, bufor) i nie zależy od danych; dekoder nie alokuje w pętli dekodowania. |
| INF-008 | Dekoder nie panikuje dla żadnego wejścia i wydaje bajty przyrostowo: wywołujący może przerwać po dowolnej liczbie bajtów wyjścia (limity ENT i SAFE działają w trakcie, nie po fakcie). |
| INF-009 | Zgodność z wyrocznią: dla danych generowanych deterministycznie i skompresowanych przez `compress/flate` na każdym poziomie oraz dla strumieni z korpusu zgodności wynik jest bajt w bajt równy wynikowi `compress/flate`. |
