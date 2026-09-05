# config-loader

A missing-key error a caller can match with errors.Is.

Bug class: error wrapping. The seed state fails `config_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
