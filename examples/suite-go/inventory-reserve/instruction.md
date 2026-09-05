`TestReserve` fails. `Reserve` takes units out of a stock level and reports
what is left and whether the order was accepted. Two rules are not being
kept:

- reserving *exactly* the stock that is left is a normal order and must be
  accepted, leaving a level of zero;
- an order for zero or a negative number of units is not an order at all
  and must be refused, leaving the level alone.

Anything larger than the stock is still refused. Fix `Reserve` in
`reserve.go`. Do not change the test.
