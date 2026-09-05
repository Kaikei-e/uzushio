# inventory-reserve

Reserving stock: the whole shelf is allowed, an empty order is not.

Bug class: wrong comparison. The seed state fails `reserve_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
