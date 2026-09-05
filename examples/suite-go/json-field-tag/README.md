# json-field-tag

A partner's JSON names, and the struct tags that have to match them.

Bug class: JSON field tag. The seed state fails `invoice_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
