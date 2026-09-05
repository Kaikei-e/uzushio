// Package store looks records up and lets a caller fall back to a default.
package store

import (
	"errors"
	"fmt"
)

// The failures a caller distinguishes.
var (
	ErrNotFound = errors.New("store: not found")
	ErrEmptyID  = errors.New("store: empty id")
)

// Store is a record store keyed by id.
type Store map[string]string

// Lookup returns the record with the given id. Both failures come back
// wrapped, with the id in the message.
func (s Store) Lookup(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("store: lookup: %w", ErrEmptyID)
	}
	v, ok := s[id]
	if !ok {
		return "", fmt.Errorf("store: lookup %q: %w", id, ErrNotFound)
	}
	return v, nil
}

// LookupOr returns the record with the given id, or def when there is no
// such record. Any other failure is passed back to the caller unchanged.
func LookupOr(s Store, id, def string) (string, error) {
	v, err := s.Lookup(id)
	if err == ErrNotFound {
		return def, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}
