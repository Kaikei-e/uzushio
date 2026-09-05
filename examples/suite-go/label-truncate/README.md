# label-truncate

Shortening a label by characters rather than by bytes.

Bug class: rune vs byte. The seed state fails `truncate_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
