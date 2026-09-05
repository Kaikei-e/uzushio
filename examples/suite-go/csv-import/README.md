# csv-import

Field cleanup: drop the byte order mark, trim every kind of whitespace.

Bug class: string trimming. The seed state fails `clean_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
