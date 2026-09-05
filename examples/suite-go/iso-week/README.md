# iso-week

ISO 8601 week labels, whose year is not always the calendar year.

Bug class: ISO week edge. The seed state fails `label_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
