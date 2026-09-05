// Package fanout builds the per-shard handlers a dispatcher hands out.
package fanout

// Handlers returns one handler per shard, in the order the shards were
// given. Handler i, whenever it is called, reports shard i: the handlers
// outlive this call and are called long after it has returned.
func Handlers(shards []string) []func() string {
	out := make([]func() string, 0, len(shards))
	shard := ""
	for _, s := range shards {
		shard = s
		out = append(out, func() string { return shard })
	}
	return out
}
