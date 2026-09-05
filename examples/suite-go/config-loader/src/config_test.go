package config

import (
	"errors"
	"strings"
	"testing"
)

func TestGetPresent(t *testing.T) {
	m := map[string]string{"db_host": "localhost"}
	got, err := Get(m, "db_host")
	if err != nil {
		t.Fatalf("Get returned %v, want no error", err)
	}
	if got != "localhost" {
		t.Errorf("Get = %q, want %q", got, "localhost")
	}
}

func TestGetMissing(t *testing.T) {
	m := map[string]string{"db_host": "localhost"}
	got, err := Get(m, "db_port")
	if err == nil {
		t.Fatal("Get on a missing key returned no error")
	}
	if got != "" {
		t.Errorf("Get returned value %q alongside an error, want the empty string", got)
	}
	if !errors.Is(err, ErrMissing) {
		t.Errorf("errors.Is(err, ErrMissing) = false for %v", err)
	}
	if !strings.Contains(err.Error(), "db_port") {
		t.Errorf("error message %q does not name the missing key", err)
	}
}
