# retry-backoff

Exponential backoff that must survive an unbounded attempt count.

Bug class: integer overflow guard. The seed state fails `backoff_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
