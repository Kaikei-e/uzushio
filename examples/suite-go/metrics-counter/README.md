# metrics-counter

An event counter shared by many goroutines, verified with -race.

Bug class: missing mutex. The seed state fails `counter_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
