# event-ring

A fixed-size event ring that reports its events oldest first.

Bug class: ring buffer wraparound. The seed state fails `ring_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
