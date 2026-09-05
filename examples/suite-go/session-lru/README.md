# session-lru

Least-recently-used eviction, where reading counts as using.

Bug class: LRU eviction order. The seed state fails `lru_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
