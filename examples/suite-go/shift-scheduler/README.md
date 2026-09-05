# shift-scheduler

Which working day a timestamp falls on, read in the site's own zone.

Bug class: time zone handling. The seed state fails `day_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
