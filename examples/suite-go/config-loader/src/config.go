// Package config reads settings out of an already parsed map.
package config

import (
	"errors"
	"fmt"
)

// ErrMissing is the sentinel a caller tests for with errors.Is when a
// setting was not supplied.
var ErrMissing = errors.New("config: missing key")

// Get returns the value stored under key. When the key is absent it returns
// an error that a caller can match against ErrMissing with errors.Is, and
// whose message names the key that was missing.
func Get(m map[string]string, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("config: missing key %q", key)
	}
	return v, nil
}
