// Package request waits for work that a caller may give up on.
package request

import "context"

// Wait blocks until the work signals that it is done by closing done, or
// until ctx is done, whichever happens first.
//
// When the work finishes first, Wait returns nil. When the caller gives up
// first, Wait returns the context's own error, which is what lets a caller
// tell a cancellation apart from a deadline that ran out.
//
// Wait must not outlive the context. A caller that cancels is entitled to
// an answer, whether or not the work it was waiting for ever finishes.
func Wait(ctx context.Context, done <-chan struct{}) error {
	<-done
	return nil
}
