`TestRow` fails. `Row` joins the fields with commas and ends the line with
CRLF, but it never quotes anything, so a field carrying a comma splits the
record in two.

A field that carries a comma, a double quote, a carriage return or a
newline must be wrapped in double quotes, and every double quote inside
such a field written twice. A field carrying none of those is written
plain, with no quotes added around it. Fix `Row` in `row.go`. Do not change
the test.
