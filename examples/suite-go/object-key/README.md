# object-key

Object-store keys, always slash-separated and always cleaned.

Bug class: path joining. The seed state fails `key_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
