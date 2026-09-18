package usb

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// ControlTransfer performs a control transfer on the device
func (h *DeviceHandle) ControlTransfer(requestType, request uint8, value, index uint16, data []byte, timeout time.Duration) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	if h.hid != nil {
		return h.hidControlTransfer(requestType, request, value, index, data)
	}

	timeoutMs := uint32(timeout.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 5000 // Default 5 second timeout
	}

	return h.devInterface.ControlTransfer(requestType, request, value, index, data, timeoutMs)
}

// BulkTransfer performs a bulk transfer on an endpoint.
//
// A zero-length data slice is rejected with ErrInvalidParameter; use
// BulkTransferWithOptions to permit zero-length packets.
func (h *DeviceHandle) BulkTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error) {
	return h.bulkTransfer(endpoint, data, timeout, false)
}

// bulkTransfer is the shared implementation behind BulkTransfer and
// BulkTransferWithOptions.
func (h *DeviceHandle) bulkTransfer(endpoint uint8, data []byte, timeout time.Duration, allowZeroLength bool) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	// Handle zero-length packets
	if len(data) == 0 && !allowZeroLength {
		return 0, ErrInvalidParameter
	}

	if h.hid != nil {
		// A HID interface has no bulk endpoints. Interrupt endpoints are
		// reached through this same call, routed to a report.
		return h.hid.interruptTransfer(endpoint, data, timeout)
	}

	// Determine interface from endpoint
	// This is simplified - need to track which interface owns which endpoint
	var intf *IOUSBInterfaceInterface
	for _, i := range h.interfaces {
		intf = i
		break
	}

	if intf == nil {
		// No interface claimed, try to auto-claim based on endpoint
		// In a real implementation, we'd need to properly map endpoints to interfaces
		return 0, fmt.Errorf("no interface claimed for endpoint %02x", endpoint)
	}

	timeoutMs := uint32(timeout.Milliseconds())
	if timeoutMs == 0 {
		timeoutMs = 5000 // Default 5 second timeout
	}

	// pipeRef is a position in the interface's own pipe table, not derivable
	// from the endpoint address without asking IOKit; see PipeRefForEndpoint.
	pipeRef, err := intf.PipeRefForEndpoint(endpoint)
	if err != nil {
		return 0, err
	}

	if endpoint&0x80 != 0 {
		return intf.BulkTransferIn(pipeRef, data, timeoutMs)
	}
	return intf.BulkTransferOut(pipeRef, data, timeoutMs)
}

// InterruptTransfer performs an interrupt transfer on an endpoint.
//
// Unlike BulkTransfer, this does not go through bulkTransfer's *TO (timeout)
// variant: confirmed against a real FX2 interrupt endpoint, ReadPipeTO
// returns kIOReturnBadArgument for interrupt-type pipes, while plain
// ReadPipe on the exact same pipe succeeds. IOKit's non-timeout
// ReadPipe/WritePipe block with no way to cancel them, so the caller's
// timeout is enforced here in software instead: the underlying call keeps
// running on its own goroutine even past the deadline, since AbortPipe (the
// only real cancellation IOKit offers) would affect every transfer queued
// on the pipe, not just this one.
func (h *DeviceHandle) InterruptTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}
	if len(data) == 0 {
		return 0, ErrInvalidParameter
	}

	if h.hid != nil {
		return h.hid.interruptTransfer(endpoint, data, timeout)
	}

	var intf *IOUSBInterfaceInterface
	for _, i := range h.interfaces {
		intf = i
		break
	}
	if intf == nil {
		return 0, fmt.Errorf("no interface claimed for endpoint %02x", endpoint)
	}

	pipeRef, err := intf.PipeRefForEndpoint(endpoint)
	if err != nil {
		return 0, err
	}

	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var n int
		var err error
		if endpoint&0x80 != 0 {
			n, err = intf.BulkTransferIn(pipeRef, data, 0)
		} else {
			n, err = intf.BulkTransferOut(pipeRef, data, 0)
		}
		done <- result{n, err}
	}()

	if timeout <= 0 {
		r := <-done
		return r.n, r.err
	}
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(timeout):
		return 0, ErrTimeout
	}
}

// Transfer represents a USB transfer
type Transfer struct {
	handle       *DeviceHandle
	endpoint     uint8
	transferType TransferType
	buffer       []byte
	timeout      time.Duration
	status       TransferStatus
	actualLength int
	callback     TransferCallback
	userData     interface{}

	// mu guards status, actualLength, buffer and callback, which an
	// AsyncTransfer's IOKit run-loop callback writes from whatever goroutine
	// services the run loop, concurrently with the submitting goroutine
	// reading them through Status, ActualLength or Buffer. Linux and Windows
	// guard the same fields on their Transfer for the same reason.
	mu sync.Mutex
}

// NewTransfer creates a new transfer.
//
// Deprecated: this whole family (NewTransfer, Transfer.Submit,
// Transfer.Cancel, SubmitTransfer, ReapTransfer, CancelTransfer) reports
// ErrNotSupported on Linux and Windows, and even here Submit only blocks
// synchronously rather than actually queuing anything, and ReapTransfer is
// unconditionally unimplemented. AsyncTransfer is the real, working
// equivalent on every platform: use NewBulkTransfer, NewInterruptTransfer
// or NewControlTransfer to create one, AsyncTransfer.Submit to queue it,
// and AsyncTransfer.Wait or WaitWithTimeout to wait for it.
func NewTransfer(handle *DeviceHandle, endpoint uint8, transferType TransferType, bufferSize int) *Transfer {
	return &Transfer{
		handle:       handle,
		endpoint:     endpoint,
		transferType: transferType,
		buffer:       make([]byte, bufferSize),
		timeout:      5 * time.Second,
		status:       TransferError,
	}
}

// SetBuffer replaces the transfer's data buffer.
func (t *Transfer) SetBuffer(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buffer = data
}

// SetTimeout sets the timeout applied when the transfer is submitted.
func (t *Transfer) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

// SetCallback sets the transfer callback
func (t *Transfer) SetCallback(callback TransferCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = callback
}

// SetUserData sets user data for the transfer
func (t *Transfer) SetUserData(data interface{}) {
	t.userData = data
}

// GetUserData gets the user data
func (t *Transfer) GetUserData() interface{} {
	return t.userData
}

// Submit submits the transfer.
//
// Deprecated: see NewTransfer's doc comment; use AsyncTransfer.Submit.
func (t *Transfer) Submit() error {
	// Simplified synchronous implementation
	// A full implementation would use async IOKit APIs

	var n int
	var err error

	switch t.transferType {
	case TransferTypeControl:
		// Control transfers would need additional setup packet data
		return fmt.Errorf("async control transfers not yet implemented")

	case TransferTypeBulk:
		n, err = t.handle.BulkTransfer(t.endpoint, t.buffer, 5*time.Second)

	case TransferTypeInterrupt:
		n, err = t.handle.InterruptTransfer(t.endpoint, t.buffer, 5*time.Second)

	case TransferTypeIsochronous:
		return fmt.Errorf("isochronous transfers not yet implemented")

	default:
		return fmt.Errorf("unknown transfer type")
	}

	t.mu.Lock()
	t.actualLength = n
	if err != nil {
		if err == ErrTimeout {
			t.status = TransferTimedOut
		} else {
			t.status = TransferError
		}
	} else {
		t.status = TransferCompleted
	}
	callback := t.callback
	t.mu.Unlock()

	if callback != nil {
		callback(t)
	}

	return err
}

// Cancel cancels the transfer.
//
// Deprecated: see NewTransfer's doc comment; use AsyncTransfer.Cancel.
func (t *Transfer) Cancel() error {
	// Cancellation would require async API support
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = TransferCancelled
	return nil
}

// Status returns the transfer status
func (t *Transfer) Status() TransferStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// ActualLength returns the actual number of bytes transferred
func (t *Transfer) ActualLength() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.actualLength
}

// Buffer returns the transfer buffer
func (t *Transfer) Buffer() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buffer
}

// Free frees the transfer resources
func (t *Transfer) Free() {
	// Nothing to free in this implementation
}

// SubmitTransfer submits a transfer for asynchronous execution.
//
// Deprecated: see NewTransfer's doc comment; use AsyncTransfer.Submit.
func (h *DeviceHandle) SubmitTransfer(transfer *Transfer) error {
	// Simplified implementation - just run synchronously for now
	return transfer.Submit()
}

// ReapTransfer waits for a completed transfer.
//
// Deprecated: see NewTransfer's doc comment; use AsyncTransfer.Wait or
// WaitWithTimeout. Unconditionally unimplemented even on macOS.
func (h *DeviceHandle) ReapTransfer(timeout time.Duration) (*Transfer, error) {
	// This would need proper async implementation
	return nil, fmt.Errorf("async transfers not fully implemented")
}

// URB structure for macOS (compatibility)
type URB struct {
	Type            uint8
	Endpoint        uint8
	Status          int32
	Flags           uint32
	Buffer          uintptr
	BufferLength    int32
	ActualLength    int32
	StartFrame      int32
	NumberOfPackets int32
	ErrorCount      int32
	SignR           uint32
	UserContext     uintptr
}

// AllocStreams allocates bulk streams (USB 3.0+).
//
// IOKit exposes no stream API, so this always reports ErrNotSupported.
func (h *DeviceHandle) AllocStreams(numStreams uint32, endpoints []uint8) error {
	return ErrNotSupported
}

// AllocateStreams allocates bulk streams (USB 3.0+).
//
// Deprecated: use AllocStreams, which is the name used on every platform.
func (h *DeviceHandle) AllocateStreams(numStreams uint32, endpoints []uint8) error {
	return h.AllocStreams(numStreams, endpoints)
}

// FreeStreams releases bulk streams (USB 3.0+).
//
// IOKit exposes no stream API, so this always reports ErrNotSupported.
func (h *DeviceHandle) FreeStreams(endpoints []uint8) error {
	return ErrNotSupported
}

// CancelTransfer requests cancellation of a previously submitted transfer.
//
// IOKit cancellation is per pipe rather than per transfer, so this aborts the
// pipe the transfer was submitted on.
//
// Deprecated: see NewTransfer's doc comment; use AsyncTransfer.Cancel.
func (h *DeviceHandle) CancelTransfer(transfer *Transfer) error {
	if transfer == nil {
		return ErrInvalidParameter
	}
	return transfer.Cancel()
}

// IsochronousTransfer performs a one-shot isochronous transfer.
//
// The macOS backend drives isochronous traffic through IsochronousTransfer
// objects instead; use NewIsochronousTransfer.
func (h *DeviceHandle) IsochronousTransfer(endpoint uint8, data []byte, numPackets int, packetSize int, timeout time.Duration) ([]IsoPacketResult, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrDeviceNotFound
	}

	if numPackets <= 0 || packetSize <= 0 {
		return nil, ErrInvalidParameter
	}

	return nil, ErrNotSupported
}

// Control transfer helpers

// GetStatus performs a GET_STATUS control request
func (h *DeviceHandle) GetStatus(recipient, index uint16) (uint16, error) {
	buf := make([]byte, 2)
	_, err := h.ControlTransfer(
		0x80|(uint8(recipient)&0x1F), // IN, standard, recipient
		USB_REQ_GET_STATUS,
		0,
		index,
		buf,
		5*time.Second,
	)
	if err != nil {
		return 0, err
	}

	return uint16(buf[0]) | (uint16(buf[1]) << 8), nil
}

// ClearFeature performs a CLEAR_FEATURE control request.
//
// requestType is the full bmRequestType byte, matching the other backends.
func (h *DeviceHandle) ClearFeature(requestType uint8, feature uint16, index uint16) error {
	_, err := h.ControlTransfer(
		requestType,
		USB_REQ_CLEAR_FEATURE,
		feature,
		index,
		nil,
		5*time.Second,
	)
	return err
}

// SetFeature performs a SET_FEATURE control request.
//
// requestType is the full bmRequestType byte, matching the other backends.
func (h *DeviceHandle) SetFeature(requestType uint8, feature uint16, index uint16) error {
	_, err := h.ControlTransfer(
		requestType,
		USB_REQ_SET_FEATURE,
		feature,
		index,
		nil,
		5*time.Second,
	)
	return err
}
