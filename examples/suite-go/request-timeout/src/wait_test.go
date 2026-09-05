package request

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitFor runs Wait and gives it two seconds to answer.
func waitFor(t *testing.T, ctx context.Context, done <-chan struct{}) error {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- Wait(ctx, done) }()
	select {
	case err := <-errc:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return")
		return nil
	}
}

func TestWaitReturnsWhenTheWorkFinishes(t *testing.T) {
	done := make(chan struct{})
	go func() { time.Sleep(10 * time.Millisecond); close(done) }()
	if err := waitFor(t, context.Background(), done); err != nil {
		t.Errorf("Wait returned %v, want nil", err)
	}
}

func TestWaitReturnsWhenTheCallerGivesUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if err := waitFor(t, ctx, make(chan struct{})); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait returned %v, want context.Canceled", err)
	}
	cancel()

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := waitFor(t, ctx, make(chan struct{})); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait returned %v, want context.DeadlineExceeded", err)
	}
}
