# request-timeout

Waiting for work, and answering the caller who gives up first.

Bug class: context cancellation. The seed state fails `wait_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
