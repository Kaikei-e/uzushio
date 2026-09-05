# feed-producer

A streamed page whose channel ends when the items run out.

Bug class: channel never closed. The seed state fails `stream_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
