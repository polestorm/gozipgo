# Wymagania: dekompresja DEFLATE

Status: przyjęte

Przyjęte w etapie 2 (2026-11-27). Decyzja właściciela z 2026-11-26 (pytanie 1): dekoder własny, w
`internal/inflate`, pisany z RFC 1951; `compress/flate` jest tylko wyrocznią w testach. Reguły, których RFC nie
rozstrzyga wprost, są w ADR 0003 pkt 10.

| Id | Wymaganie |
|---|---|
| INF-001 | Dekoder daje dla każdego poprawnego strumienia DEFLATE dokładnie te bajty, które wyznacza RFC 1951: bloki niezakodowane (BTYPE 00, 3.2.4), ze stałymi kodami Huffmana (01, 3.2.6) i z dynamicznymi (10, 3.2.7), w dowolnej kolejności, z odwołaniami do okna 32 KiB także przez granice bloków i z odwołaniami zachodzącymi na własny cel (3.2.3). |
| INF-002 | BTYPE 11 (3.2.3) daje `inflate-block-type`. |
| INF-003 | Blok niezakodowany (3.2.4): NLEN musi być dopełnieniem LEN (`inflate-stored-length`); bity do granicy bajtu przed LEN są pomijane, jakąkolwiek mają wartość. |
| INF-004 | Kody dynamiczne (3.2.7): HLIT najwyżej 286 i HDIST najwyżej 30; kod długości kodów i kod znaków muszą być zupełne (ani nadmiarowe, ani z nieużytymi kodami); kod odległości jest zupełny, pusty (blok samych znaków) albo ma jeden kod o długości 1; powtórzenie długości (symbole 16–18) nie może wyjść poza HLIT + HDIST długości ani zaczynać się bez poprzednika; kod końca bloku (256) musi mieć długość niezerową. Naruszenia dają `inflate-code-lengths`. |
| INF-005 | Symbole długości 286–287 i odległości 30–31 (3.2.6), odwołanie w bloku bez kodów odległości, ciąg bitów, który nie jest żadnym kodem niezupełnego kodu odległości, oraz odległość większa niż liczba dotąd wydanych bajtów (3.2.3) dają `inflate-distance`. |
| INF-006 | Koniec wejścia przed końcem ostatniego bloku daje `inflate-truncated`. Dekoder podaje liczbę zużytych bajtów wejścia; bajty wejścia po końcu ostatniego bloku wykrywa wywołujący (`inflate-trailing-data`, ENT-007). |
| INF-007 | Pamięć dekodera jest stała (okno, tablice kodów, bufor wejścia) i nie zależy od danych; dekoder nie alokuje w czasie dekodowania. |
| INF-008 | Dekoder nie panikuje dla żadnego wejścia i wydaje bajty przyrostowo: wywołujący może przerwać po dowolnej liczbie bajtów wyjścia. Bajty zdekodowane przed wadą strumienia są wydawane przed błędem, więc ich liczba nie zależy od rozmiaru buforów. Błąd źródła wraca bez zmiany. |
| INF-009 | Zgodność z wyrocznią: dla danych generowanych deterministycznie i skompresowanych przez `compress/flate` na każdym poziomie oraz dla strumieni z korpusu zgodności wynik jest bajt w bajt równy danym źródłowym. Strumień przyjęty przez dekoder przyjmuje też `compress/flate`, z tym samym wynikiem (`FuzzInflate`). |
