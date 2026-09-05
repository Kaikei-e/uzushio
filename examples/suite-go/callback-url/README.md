# callback-url

A redirect URL whose two parameters survive whatever is in them.

Bug class: URL query escaping. The seed state fails `callback_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
