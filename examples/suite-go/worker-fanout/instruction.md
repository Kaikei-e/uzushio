`TestHandlersReportTheirOwnShard` fails: every handler reports the last
shard. The handlers are closures, and they all close over the *same*
variable, which the loop overwrites on each turn; by the time a handler is
called the variable holds whatever was assigned last.

Make each handler in `fanout.go` capture its own shard, so that handler i
reports shard i whenever it is called, in any order and long after
`Handlers` has returned. Do not change the test.
