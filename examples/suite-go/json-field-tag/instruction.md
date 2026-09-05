Both tests fail. `Invoice` is decoded from, and encoded back to, a
partner's JSON, whose names are lower_snake_case throughout: `number`,
`customer`, `amount_cents`, `paid`. Two fields do not carry that name — one
tag is misspelt and one field has no tag at all, so it is encoded under its
Go name.

Fix the struct tags in `invoice.go` so that the payload decodes into every
field and the invoice encodes back under exactly those four names. Do not
change the test.
