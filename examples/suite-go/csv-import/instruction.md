`TestClean` fails. `Clean` tidies one field of an uploaded CSV. It has to
do two things and only those two:

- if the field starts with a UTF-8 byte order mark (U+FEFF), drop that mark;
- then remove whitespace of every kind — spaces, tabs, carriage returns and
  newlines — from both ends.

A mark followed by spaces leaves neither behind. Whitespace *inside* the
field is part of the value and stays exactly as it is, so "Ada  Lovelace"
keeps its two spaces. Fix `Clean` in `clean.go`. Do not change the test.
