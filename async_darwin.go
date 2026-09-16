//go:build darwin && cgo

// This file reaches IOKit through cgo. The pure-Go backend selected when cgo is
// disabled lives in the purego_*_darwin.go files; see issue #14.

package usb

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>

void RunLoopRunWithTimeout(double seconds) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, seconds, true);
}

void AddSourceToRunLoop(CFRunLoopSourceRef source) {
    CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
}

void RemoveSourceFromRunLoop(CFRunLoopSourceRef source) {
    CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
)

// AsyncTransfer represents an asynchronous USB transfer on macOS
type AsyncTransfer struct {
	*Transfer
	handle    *DeviceHandle
	submitted bool
	completed bool
	mutex     sync.Mutex

	// done is closed exactly once, when the transfer completes or is
	// cancelled, so that waiters block rather than poll. See markCompleted.
	done chan struct{}
}

// NewAsyncTransfer creates a new async transfer
func NewAsyncTransfer(handle *DeviceHandle, endpoint uint8, transferType TransferType, bufferSize int) *AsyncTransfer {
	return &AsyncTransfer{
		Transfer: &Transfer{
			handle:       handle,
			endpoint:     endpoint,
			transferType: transferType,
			buffer:       make([]byte, bufferSize),
			status:       TransferError,
		},
		handle: handle,
		done:   make(chan struct{}),
	}
}

// Submit submits the async transfer
func (t *AsyncTransfer) Submit() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.submitted {
		return fmt.Errorf("transfer already submitted")
	}

	if t.handle.closed {
		return fmt.Errorf("device is closed")
	}

	// Find the interface for this endpoint
	var intf *IOUSBInterfaceInterface
	for _, i := range t.handle.interfaces {
		intf = i
		break
	}

	if intf == nil {
		return fmt.Errorf("no interface claimed for endpoint %02x", t.endpoint)
	}

	// Create async event source if needed
	if t.handle.asyncSource == 0 {
		source, err := intf.CreateAsyncEventSource()
		if err != nil {
			return err
		}
		t.handle.asyncSource = source
		C.AddSourceToRunLoop(source)
	}

	// Submit the async transfer
	callback := func(result int32, bytesTransferred uint32) {
		t.mutex.Lock()
		defer t.mutex.Unlock()

		// t.Transfer.mu (distinct from t.mutex above) guards status,
		// actualLength and callback, because Transfer.Status, ActualLength
		// and Buffer are read directly by the submitting goroutine without
		// going through AsyncTransfer at all.
		t.Transfer.mu.Lock()
		t.actualLength = int(bytesTransferred)
		if result == kIOReturnSuccess {
			t.status = TransferCompleted
		} else if result == int32(kIOUSBTransactionTimeout) {
			t.status = TransferTimedOut
		} else {
			t.status = TransferError
		}
		userCallback := t.callback
		t.Transfer.mu.Unlock()

		t.markCompletedLocked()

		if userCallback != nil {
			userCallback(t.Transfer)
		}
	}

	var err error
	pipeRef := t.endpoint & 0x0F

	if t.endpoint&0x80 != 0 {
		// IN transfer
		err = intf.BulkTransferInAsync(pipeRef, t.buffer, callback)
	} else {
		// OUT transfer
		err = intf.BulkTransferOutAsync(pipeRef, t.buffer, callback)
	}

	if err != nil {
		return err
	}

	t.submitted = true
	return nil
}

// Wait and WaitWithTimeout are defined in compat_darwin.go so that they stay in
// step with the cross-platform contract.

// Cancel cancels the async transfer
func (t *AsyncTransfer) Cancel() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if !t.submitted {
		return fmt.Errorf("transfer not submitted")
	}

	if t.completed {
		return nil
	}

	// Note: Proper cancellation would require IOKit async API support
	t.Transfer.mu.Lock()
	t.status = TransferCancelled
	t.Transfer.mu.Unlock()
	t.markCompletedLocked()

	return nil
}

// IsCompleted checks if the transfer is completed
func (t *AsyncTransfer) IsCompleted() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.completed
}

// AsyncBulkTransfer performs an asynchronous bulk transfer
func (h *DeviceHandle) AsyncBulkTransfer(endpoint uint8, data []byte, callback func(*Transfer)) error {
	transfer := NewAsyncTransfer(h, endpoint, TransferTypeBulk, len(data))
	copy(transfer.buffer, data)
	transfer.SetCallback(callback)

	return transfer.Submit()
}

// AsyncInterruptTransfer performs an asynchronous interrupt transfer
func (h *DeviceHandle) AsyncInterruptTransfer(endpoint uint8, data []byte, callback func(*Transfer)) error {
	transfer := NewAsyncTransfer(h, endpoint, TransferTypeInterrupt, len(data))
	copy(transfer.buffer, data)
	transfer.SetCallback(callback)

	return transfer.Submit()
}

// HandleEvents processes pending USB events
func HandleEvents(timeout time.Duration) error {
	// Run the CFRunLoop to process async events
	seconds := timeout.Seconds()
	if seconds <= 0 {
		seconds = 0.001 // Minimum timeout
	}

	C.RunLoopRunWithTimeout(C.double(seconds))
	return nil
}

// RunEventLoop runs the event loop in a separate goroutine
func RunEventLoop(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
			HandleEvents(10 * time.Millisecond)
		}
	}
}

// Note: A full implementation would require:
// 1. Integration with CFRunLoop for proper async event handling
// 2. Use of IOUSBInterfaceInterface's async methods (ReadPipeAsync, WritePipeAsync)
// 3. Proper callback registration with IOKit
// 4. Thread-safe transfer queue management
