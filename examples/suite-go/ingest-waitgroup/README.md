# ingest-waitgroup

Scoring every item at once, and waiting for all of it.

Bug class: WaitGroup.Add placement. The seed state fails `sum_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
