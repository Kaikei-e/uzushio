# worker-fanout

One handler per shard, each remembering its own shard.

Bug class: closure over a shared variable. The seed state fails
`fanout_test.go`; `reference.diff` is the fix, and `mutants/` holds three
plausible wrong fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
