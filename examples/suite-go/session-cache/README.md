# session-cache

Role lookup that must answer false rather than panic on anything missing.

Bug class: missing nil check. The seed state fails `cache_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
