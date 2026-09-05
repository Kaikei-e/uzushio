# store-notfound

Falling back to a default on a missing record, and only on that.

Bug class: error sentinel comparison. The seed state fails `store_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
