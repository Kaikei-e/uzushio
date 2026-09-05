`TestGetMissing` fails. When a key is absent, `Get` returns an error, but
callers need two things from it at once:

- `errors.Is(err, ErrMissing)` must report true, so a caller can tell a
  missing setting apart from any other failure;
- the message must still name the key that was missing, so a log line is
  worth reading.

Fix `Get` in `config.go` so a single error does both. Do not change the
test.
