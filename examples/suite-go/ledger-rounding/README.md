# ledger-rounding

Money is rounded to whole cents; halfway amounts must go away from zero.

Bug class: rounding rule. The seed state fails `rounding_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
