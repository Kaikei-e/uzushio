# poll-interval

Turning polls per minute into the wait between two polls.

Bug class: time.Duration unit. The seed state fails `interval_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
