package store

import (
	"errors"
	"testing"
)

var s = Store{"a": "apple"}

func TestLookupOrFound(t *testing.T) {
	got, err := LookupOr(s, "a", "none")
	if err != nil || got != "apple" {
		t.Errorf(`LookupOr(s, "a", "none") = (%q, %v), want ("apple", nil)`, got, err)
	}
}

func TestLookupOrMissingUsesTheDefault(t *testing.T) {
	got, err := LookupOr(s, "b", "none")
	if err != nil {
		t.Fatalf(`LookupOr(s, "b", "none") returned %v, want no error`, err)
	}
	if got != "none" {
		t.Errorf(`LookupOr(s, "b", "none") = %q, want "none"`, got)
	}
}

func TestLookupOrPassesOtherFailuresOn(t *testing.T) {
	got, err := LookupOr(s, "", "none")
	if err == nil {
		t.Fatalf(`LookupOr(s, "", "none") = %q, want an error`, got)
	}
	if !errors.Is(err, ErrEmptyID) {
		t.Errorf("error %v does not match ErrEmptyID", err)
	}
	if got != "" {
		t.Errorf(`LookupOr(s, "", "none") = %q alongside its error, want ""`, got)
	}
}
