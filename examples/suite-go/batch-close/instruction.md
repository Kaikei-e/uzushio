`TestTotalClosesEachBatchBeforeTheNext` fails: opening the second batch is
refused because the first is still open. `Total` defers the close, and a
deferred call runs when the *function* returns, not when the loop turn
ends, so every batch stays open until the very end.

Close each batch when its turn is over, before the next one is opened, and
still leave no batch open when `Total` returns. A batch reports no records
once it is closed, so it has to be read before it is closed. Fix `Total` in
`batch.go`. Do not change the test.
