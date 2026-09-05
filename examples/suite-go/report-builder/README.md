# report-builder

A renderer reused for every section, one section at a time.

Bug class: builder reuse. The seed state fails `render_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
