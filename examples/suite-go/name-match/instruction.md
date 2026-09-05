`TestSame` fails. `Same` lowers both names and compares them, which is not
the same as case folding and is not what the documentation promises. A
Greek word ending in a final sigma "ς" and the same word written with a
medial "σ" are one name, and lowering leaves them different; letters that
merely look alike, such as the dotless "ı" and "I", are *not* one letter,
and something coarser than folding would merge them.

Spaces around a name are noise; spaces inside it are part of it. Fix `Same`
in `match.go`. Do not change the test.
