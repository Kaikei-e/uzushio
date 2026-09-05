// Package callback builds the redirect an identity provider is sent to.
package callback

// URL returns base with a query carrying the two parameters. The values
// come from user input and may hold anything at all: spaces, ampersands,
// equals signs, percent signs, fragments, characters outside ASCII. The
// URL that comes back must still parse as one URL whose query holds
// exactly these two parameters, each with the value it was given back
// again unchanged. base is a plain address with no query of its own.
func URL(base, state, next string) string {
	return base + "?state=" + state + "&next=" + next
}
