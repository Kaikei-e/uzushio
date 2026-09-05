# tariff-parser

Parsing a semicolon-separated settings line whose values may contain '='.

Bug class: parser. The seed state fails `parse_test.go`; `reference.diff` is
the fix, and `mutants/` holds three plausible wrong fixes the test has to
reject.

```sh
./setup.sh && uzushio task doctor --task .
```
