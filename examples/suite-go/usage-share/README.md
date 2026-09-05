# usage-share

A percentage computed from two counters, in real arithmetic.

Bug class: integer vs float division. The seed state fails `share_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
