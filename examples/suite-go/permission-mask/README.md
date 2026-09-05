# permission-mask

Permission bits numbered from zero, packed into one byte.

Bug class: bit mask off-by-one. The seed state fails `mask_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
