package usb

import (
	"testing"
	"time"
)

// These tests exercise the macOS asynchronous wait machinery without a device.
// They compile and run under both macOS backends, since AsyncTransfer has the
// same shape in each.

// TestAsyncTransferWaitReturnsPromptlyOnCompletion is the regression test for the
// wait implementation. It previously polled at 10 ms, so a completion could take
// up to a full interval to be noticed; waiting on a channel makes it immediate.
//
// The 5 ms budget is comfortably below the old poll interval, so this test fails
// against a polling implementation and passes against a blocking one.
func TestAsyncTransferWaitReturnsPromptlyOnCompletion(t *testing.T) {
	transfer := NewAsyncTransfer(nil, 0x81, TransferTypeBulk, 8)

	transfer.mutex.Lock()
	transfer.markCompletedLocked()
	transfer.mutex.Unlock()

	start := time.Now()
	if err := transfer.Wait(); err != nil {
		t.Fatalf("Wait returned %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Errorf("Wait took %v after completion, want under 5ms; this suggests polling", elapsed)
	}
}

// TestAsyncTransferWaitUnblocksWhenCompletedConcurrently checks that a waiter
// blocked before completion is released by it, which is the case that matters
// for a transfer completing on IOKit's run loop.
func TestAsyncTransferWaitUnblocksWhenCompletedConcurrently(t *testing.T) {
	transfer := NewAsyncTransfer(nil, 0x81, TransferTypeBulk, 8)

	waited := make(chan error, 1)
	go func() {
		waited <- transfer.Wait()
	}()

	// Give the waiter a moment to block, then complete the transfer.
	time.Sleep(10 * time.Millisecond)
	transfer.mutex.Lock()
	transfer.markCompletedLocked()
	transfer.mutex.Unlock()

	select {
	case err := <-waited:
		if err != nil {
			t.Errorf("Wait returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after the transfer completed")
	}
}

func TestAsyncTransferWaitWithTimeout(t *testing.T) {
	t.Run("times out when nothing completes", func(t *testing.T) {
		transfer := NewAsyncTransfer(nil, 0x81, TransferTypeBulk, 8)

		start := time.Now()
		err := transfer.WaitWithTimeout(40 * time.Millisecond)
		elapsed := time.Since(start)

		if err != ErrTimeout {
			t.Errorf("WaitWithTimeout returned %v, want ErrTimeout", err)
		}
		if elapsed < 40*time.Millisecond {
			t.Errorf("returned after %v, want at least the 40ms timeout", elapsed)
		}
		if elapsed > 500*time.Millisecond {
			t.Errorf("returned after %v, far longer than the 40ms timeout", elapsed)
		}
	})

	t.Run("returns immediately when already completed", func(t *testing.T) {
		transfer := NewAsyncTransfer(nil, 0x81, TransferTypeBulk, 8)

		transfer.mutex.Lock()
		transfer.markCompletedLocked()
		transfer.mutex.Unlock()

		if err := transfer.WaitWithTimeout(time.Second); err != nil {
			t.Errorf("WaitWithTimeout returned %v, want nil", err)
		}
	})
}

// TestAsyncTransferMarkCompletedIsIdempotent guards the close-once invariant.
// Both the IOKit completion callback and Cancel can reach this path, and closing
// an already-closed channel panics.
func TestAsyncTransferMarkCompletedIsIdempotent(t *testing.T) {
	transfer := NewAsyncTransfer(nil, 0x81, TransferTypeBulk, 8)

	transfer.mutex.Lock()
	transfer.markCompletedLocked()
	transfer.markCompletedLocked()
	transfer.markCompletedLocked()
	transfer.mutex.Unlock()

	if !transfer.IsCompleted() {
		t.Error("IsCompleted reported false after completion")
	}
	if err := transfer.Wait(); err != nil {
		t.Errorf("Wait returned %v, want nil", err)
	}
}

// TestAsyncTransferWaitWithoutConstructor checks that a zero-value transfer,
// which has no completion channel, reports an error rather than blocking forever.
func TestAsyncTransferWaitWithoutConstructor(t *testing.T) {
	var transfer AsyncTransfer

	if err := transfer.Wait(); err != ErrInvalidParameter {
		t.Errorf("Wait returned %v, want ErrInvalidParameter", err)
	}
	if err := transfer.WaitWithTimeout(time.Millisecond); err != ErrInvalidParameter {
		t.Errorf("WaitWithTimeout returned %v, want ErrInvalidParameter", err)
	}
}
