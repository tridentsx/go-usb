//go:build darwin && !cgo

// Asynchronous bulk and interrupt transfers via IOKit, without cgo.
//
// Shares the per-interface async pump isochronous transfers use (see
// ensureAsyncPump in purego_isochronous_darwin.go): one goroutine per
// interface, locked to an OS thread, servicing a single CFRunLoop that
// carries every asynchronous completion for that interface, whichever kind.
//
// ReadPipeAsync/WritePipeAsync's completion differs from
// ReadIsochPipeAsync/WriteIsochPipeAsync's in exactly the field that matters
// for correlating a completion back to its transfer: IOKit documents arg0 as
// the byte count here, not a pointer, so refcon -- the one field IOKit passes
// through unexamined -- carries the correlation key instead. That key is the
// address of the transfer's own buffer, which the pendingBulk map on
// IOUSBInterfaceInterface keeps a live *AsyncTransfer reference for from
// Submit until completion, for the same reason the isochronous pending map
// does: it is what keeps the buffer reachable for as long as IOKit holds a
// raw pointer to it, without runtime.Pinner or any cgo pointer-passing rule.
//
// See issue #14.

package usb

import (
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// AsyncTransfer represents an asynchronous USB transfer on macOS.
type AsyncTransfer struct {
	*Transfer
	handle    *DeviceHandle
	submitted bool
	completed bool
	mutex     sync.Mutex

	// intfRef is the interface Submit resolved and submitted through, needed
	// by Cancel to find the same pipe again.
	intfRef *IOUSBInterfaceInterface

	// done is closed exactly once, when the transfer completes or is
	// cancelled, so that waiters block rather than poll. See
	// markCompletedLocked in compat_darwin.go.
	done chan struct{}
}

// NewAsyncTransfer creates a new async transfer.
//
// Deprecated: use DeviceHandle.NewBulkTransfer, NewInterruptTransfer or
// NewControlTransfer, which report allocation errors.
func NewAsyncTransfer(handle *DeviceHandle, endpoint uint8, transferType TransferType, bufferSize int) *AsyncTransfer {
	return &AsyncTransfer{
		Transfer: &Transfer{
			handle:       handle,
			endpoint:     endpoint,
			transferType: transferType,
			buffer:       make([]byte, bufferSize),
			timeout:      5 * time.Second,
			status:       TransferError,
		},
		handle: handle,
		done:   make(chan struct{}),
	}
}

// IsCompleted reports whether the transfer has completed. It never blocks.
func (t *AsyncTransfer) IsCompleted() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.completed
}

// Submit queues the transfer and returns immediately; it is not complete
// when this returns. Call Wait or WaitWithTimeout.
func (t *AsyncTransfer) Submit() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.submitted {
		return fmt.Errorf("transfer already submitted")
	}
	if t.handle == nil || t.handle.closed {
		return ErrDeviceNotFound
	}
	if t.transferType == TransferTypeControl {
		return ErrNotSupported // async control transfers need DeviceRequestAsync, not ReadPipeAsync/WritePipeAsync.
	}

	// Endpoint is not tracked to a specific claimed interface, the same
	// simplification bulkTransfer and the isochronous Submit already make.
	var intf *IOUSBInterfaceInterface
	for _, i := range t.handle.interfaces {
		intf = i
		break
	}
	if intf == nil {
		return fmt.Errorf("no interface claimed for endpoint %02x", t.endpoint)
	}

	if err := intf.ensureAsyncPump(); err != nil {
		return fmt.Errorf("starting async pump: %w", err)
	}

	pipeRef, err := intf.PipeRefForEndpoint(t.endpoint)
	if err != nil {
		return err
	}

	key := bufferKey(t.buffer)
	intf.pendingBulkMu.Lock()
	intf.pendingBulk[key] = t
	intf.pendingBulkMu.Unlock()

	var submitErr error
	if t.endpoint&0x80 != 0 {
		submitErr = intf.readPipeAsync(pipeRef, t.buffer, intf.bulkCallback, key)
	} else {
		submitErr = intf.writePipeAsync(pipeRef, t.buffer, intf.bulkCallback, key)
	}
	if submitErr != nil {
		intf.pendingBulkMu.Lock()
		delete(intf.pendingBulk, key)
		intf.pendingBulkMu.Unlock()
		return fmt.Errorf("submitting async pipe %#x: %w", t.endpoint, submitErr)
	}

	t.intfRef = intf
	t.submitted = true
	return nil
}

// Cancel aborts the pipe this transfer is on, the same real cancellation
// IsochronousTransfer.Cancel uses and for the same reason: IOKit offers no
// way to cancel one specific in-flight request without affecting others
// queued on the same pipe. The aborted transfer still completes normally
// through the callback, now with an error status.
func (t *AsyncTransfer) Cancel() error {
	t.mutex.Lock()
	if !t.submitted {
		t.mutex.Unlock()
		return fmt.Errorf("transfer not submitted")
	}
	if t.completed {
		t.mutex.Unlock()
		return nil
	}
	intf := t.intfRef
	endpoint := t.endpoint
	t.mutex.Unlock()

	pipeRef, err := intf.PipeRefForEndpoint(endpoint)
	if err != nil {
		return err
	}
	return intf.AbortPipe(pipeRef)
}

// AsyncBulkTransfer submits a bulk transfer and reports completion via callback.
func (h *DeviceHandle) AsyncBulkTransfer(endpoint uint8, data []byte, callback func(*Transfer)) error {
	t, err := h.newAsyncTransfer(endpoint, TransferTypeBulk, len(data))
	if err != nil {
		return err
	}
	copy(t.buffer, data)
	t.SetCallback(callback)
	return t.Submit()
}

// AsyncInterruptTransfer submits an interrupt transfer and reports completion
// via callback.
func (h *DeviceHandle) AsyncInterruptTransfer(endpoint uint8, data []byte, callback func(*Transfer)) error {
	t, err := h.newAsyncTransfer(endpoint, TransferTypeInterrupt, len(data))
	if err != nil {
		return err
	}
	copy(t.buffer, data)
	t.SetCallback(callback)
	return t.Submit()
}

// completeAsync records one completion, delivered by the interface's shared
// bulk/interrupt callback trampoline, and wakes anything blocked in Wait.
func (t *AsyncTransfer) completeAsync(result int32, actualLength uint32) {
	t.mutex.Lock()

	t.actualLength = int(actualLength)
	switch result {
	case kernSuccess:
		t.status = TransferCompleted
	case kIOUSBTransactionTimeout:
		t.status = TransferTimedOut
	default:
		t.status = TransferError
	}
	callback := t.callback
	t.markCompletedLocked()

	t.mutex.Unlock()

	if callback != nil {
		callback(t.Transfer)
	}
}

// bufferKey is the pendingBulk map key for a transfer: the address of its
// buffer's first element, which purego.SyscallN is given as refcon in
// readPipeAsync/writePipeAsync and IOKit passes back unexamined.
func bufferKey(buf []byte) uintptr {
	if len(buf) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&buf[0]))
}
