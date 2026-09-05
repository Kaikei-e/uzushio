`TestPage` fails. `Page` hands out a window of ids and the cursor that asks
for the window after it. A caller walks the list by passing the cursor back
until there is nothing left, so the cursor must say *stop* — the value -1 —
as soon as the window it came with reached the last id. A page that ends
exactly on the last id is the last page; there is no empty page after it.

Fix `Page` in `page.go` so that walking a five-id list two at a time ends
after the id "e" rather than one call later. Do not change the test.
