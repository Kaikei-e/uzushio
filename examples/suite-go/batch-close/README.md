# batch-close

Walking batches when the store allows only one open at a time.

Bug class: defer inside a loop. The seed state fails `batch_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
