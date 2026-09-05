# version-compare

Ordering MAJOR.MINOR.PATCH versions as numbers, not as text.

Bug class: version comparison. The seed state fails `compare_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
