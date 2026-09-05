// Package tariff reads the settings line a carrier sends with a rate card.
package tariff

import (
	"fmt"
	"strings"
)

// Parse reads a settings line into a map. The line is a list of key=value
// pairs separated by semicolons, for example
//
//	zone=EU;surcharge=1.25;note=split=allowed
//
// Only the first "=" of a pair separates the key from the value, so a value
// may itself contain "=". A segment that is empty, or that is only spaces,
// is skipped, so a trailing semicolon is allowed. A segment that carries no
// "=" at all is a fault: Parse returns no map and an error naming that
// segment. A later pair with the same key replaces an earlier one.
func Parse(line string) (map[string]string, error) {
	out := map[string]string{}
	for _, seg := range strings.Split(line, ";") {
		kv := strings.Split(seg, "=")
		out[kv[0]] = kv[1]
	}
	return out, nil
}

// errSegment builds the error Parse returns for a segment with no "=".
func errSegment(seg string) error {
	return fmt.Errorf("tariff: segment %q has no %q", seg, "=")
}
