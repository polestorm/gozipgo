# Wymagania: nazwy wpisów

Status: przyjęte

Przegląd z tekstem APPNOTE 6.3.10: 2026-11-26 (etap 1). Decyzje: ADR 0001 pkt 5, ADR 0002 pkt 6–10.

| Id | Wymaganie |
|---|---|
| NAME-001 | Nazwa nie może być pusta (`name-empty`; APPNOTE 4.4.10 dopuszcza długość zero dla wpisu ze standardowego wejścia, my nie) ani zawierać bajtu zerowego (`name-nul`). |
| NAME-002 | Z bitem 11 nazwa musi być poprawnym UTF-8 (APPNOTE 4.4.4, dodatek D.2); bez bitu 11 dozwolone są tylko bajty ASCII. Inaczej `name-encoding`. Opcja `UnflaggedNames` pozwala czytać nazwy bez bitu 11 spoza ASCII jako CP437 (według tablicy), jako UTF-8 (gdy są poprawnym UTF-8) albo według strony kodowej podanej przez wywołującego (`CodePage`: 128 znaków dla bajtów 0x80–0xFF; znak poniżej U+0080 albo wartość spoza Unicode w tablicy, tak jak brak tablicy, daje `name-encoding`); jej zadziałanie jest raportowane przez `Relaxed()`. W każdym kodowaniu znak poniżej U+0020 i U+007F daje `name-control-character`. |
| NAME-003 | Separator to `/`. Odwrotny ukośnik w nazwie daje `name-backslash` (APPNOTE 4.4.17.1). |
| NAME-004 | Nazwa nie może zaczynać się od `/` ani od litery dysku z dwukropkiem (`name-absolute`; APPNOTE 4.4.17.1), ani zawierać segmentu `..` albo `.`, ani pustego segmentu (`a//b`) (`name-traversal`). Sprawdzenia biegną w stałej kolejności: pusta, bajt zerowy, kodowanie, znak sterujący, odwrotny ukośnik, ścieżka bezwzględna, segmenty. |
| NAME-005 | Dwie identyczne nazwy w katalogu dają `name-duplicate` z indeksami obu wpisów. Plik `a` i wpis katalogu `a/`, albo plik `a` i wpis `a/b`, też. Opcja `FoldCase` rozszerza porównanie na wielkość liter ASCII. |
| NAME-007 | Długość nazwy, komentarza i pól dodatkowych podlega limitom (`limit-name-length`, `limit-comment-length`, `limit-extra-length`). |
| NAME-008 | Pole dodatkowe Unicode Path (0x7075, APPNOTE 4.6.9), jeśli występuje, musi mieć wersję 1, CRC-32 pola nazwy i dawać tę samą nazwę co pole nazwy po zdekodowaniu; inaczej `name-unicode-path`. |
| NAME-009 | Wykrywanie powtórzeń i sprawdzanie nazw jest liniowo-logarytmiczne względem liczby wpisów, liniowe względem długości nazw i nie zależy od kolejności iteracji po mapie. |

Status: przyjęte

Przyjęte w etapie 2 (2026-11-27).

| Id | Wymaganie |
|---|---|
| NAME-006 | Nazwa w nagłówku lokalnym musi być bajt w bajt równa nazwie w katalogu centralnym, a bit 11 taki sam; inaczej `zip-local-header-mismatch`. |
