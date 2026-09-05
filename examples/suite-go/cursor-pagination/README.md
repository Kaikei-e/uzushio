# cursor-pagination

A cursor-paged window over a slice, and the cursor that follows it.

Bug class: off-by-one at a boundary. The seed state fails `page_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
