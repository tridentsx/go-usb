package usb

import (
	"sync"
	"testing"
	"time"
)

// TestAsyncTransferWaitHonorsConfiguredTimeout is a regression test for a
// real bug found on real hardware: plain Wait() called waitForReaping
// directly and never consulted t.timeout at all (only WaitWithTimeout's
// own explicit argument did), so a transfer whose URB never completes --
// and nothing here ever will, since this never calls Submit on a real
// device -- blocked Wait() forever regardless of SetTimeout. Given
// AsyncTransferInterface has both SetTimeout and WaitWithTimeout as
// separate methods, Wait must be the one that honors the former by
// default; otherwise SetTimeout would have no real caller-visible effect
// on the plain Wait path at all.
//
// Constructed directly rather than via NewBulkTransfer/Submit, since usbfs
// URBs need a real device fd this test has no access to; submitted stays
// false, so Wait's internal Cancel call on timeout takes its early
// "transfer not submitted" path rather than touching t.handle (nil here).
func TestAsyncTransferWaitHonorsConfiguredTimeout(t *testing.T) {
	transfer := &AsyncTransfer{
		reapCond: sync.NewCond(&sync.Mutex{}),
		timeout:  40 * time.Millisecond,
	}

	start := time.Now()
	err := transfer.Wait()
	elapsed := time.Since(start)

	if err != ErrTimeout {
		t.Errorf("Wait returned %v, want ErrTimeout", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("returned after %v, want at least the configured 40ms timeout", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("returned after %v, far longer than the configured 40ms timeout", elapsed)
	}
}
