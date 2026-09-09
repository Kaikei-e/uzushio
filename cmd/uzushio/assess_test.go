package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssessRefusesBeforeClaimingHoldout(t *testing.T) {
	root := t.TempDir()
	card := filepath.Join(root, "card.json")
	if err := os.WriteFile(card, []byte(`{"schema_version":1,"id":"incomplete"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, out := filepath.Join(root, "registry"), filepath.Join(root, "out")
	for _, args := range [][]string{
		{"judge", "assess"},
		{"judge", "assess", "--card", card, "--out", out},
		{"judge", "assess", "--card", card, "--out", out, "--registry", registry},
	} {
		got := run(t, args...)
		if got.code == exitOK || got.stdout != "" {
			t.Fatalf("%v: %+v", args, got)
		}
		for _, path := range []string{registry, out} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid assessment created %s: %v", path, err)
			}
		}
	}
}
