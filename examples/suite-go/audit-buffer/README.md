# audit-buffer

Appending to an audit trail without the two results sharing an array.

Bug class: slice aliasing. The seed state fails `buffer_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
