// AsyncTransfer's real I/O submission and completion collection go through
// a single shared I/O completion port per DeviceHandle (see
// registerAsyncCompletion and iocpPump in device_windows.go), the same
// architecture libusb's own windows_winusb.c backend uses (confirmed
// against its real source: one CreateIoCompletionPort(..., 1) plus one
// dedicated thread blocked in GetQueuedCompletionStatus, servicing every
// outstanding transfer). This replaced an earlier version of this file
// where Submit spawned a new goroutine per call, each of which created its
// own throwaway Win32 Event object and blocked on WaitForSingleObject for
// that one transfer alone: correct, but for N transfers in flight --
// keeping many buffers queued for continuous high-throughput capture, e.g.
// a logic analyzer, is the normal case for genuinely async I/O -- that
// meant N OS threads and N kernel Event objects where one shared thread
// and zero per-call Event objects suffice.
//
// HID devices are the one exception: hid.dll's own transport has no IOCP
// integration in this package, so Submit still wraps it in a per-call
// goroutine below. HID reports are small and interrupt-driven, not the
// continuous high-throughput case this rewrite targets.

package usb

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AsyncTransfer is the Windows implementation of asynchronous USB transfers.
// The type satisfies AsyncTransferInterface (asserted in
// api_contract_async.go).
type AsyncTransfer struct {
	handle       *DeviceHandle
	endpoint     uint8
	transferType TransferType
	buf          []byte
	timeout      time.Duration

	mu        sync.Mutex
	cond      *sync.Cond
	submitted bool
	done      bool
	xferred   int
	lastErr   error

	// overlapped is owned by this transfer and reused across
	// resubmissions, registered by its own address with the handle's
	// shared completion port for the duration of each Submit. Must be
	// zeroed before each reuse (Win32 requirement for a reused OVERLAPPED)
	// and must not move once registered, which is why it is a field here
	// rather than a stack-local passed down -- iocpPump dispatches
	// completions by this exact address.
	overlapped windows.Overlapped
}

// NewBulkTransfer creates an async bulk transfer with a pre-allocated buffer
// of bufferSize bytes.
func (h *DeviceHandle) NewBulkTransfer(endpoint uint8, bufferSize int) (*AsyncTransfer, error) {
	return h.newAsyncTransfer(endpoint, TransferTypeBulk, bufferSize)
}

// NewInterruptTransfer creates an async interrupt transfer with a pre-allocated
// buffer of bufferSize bytes.
func (h *DeviceHandle) NewInterruptTransfer(endpoint uint8, bufferSize int) (*AsyncTransfer, error) {
	return h.newAsyncTransfer(endpoint, TransferTypeInterrupt, bufferSize)
}

// NewControlTransfer creates an async control transfer. bufferSize must be at
// least 8 (the setup-packet header). The caller fills the buffer before Submit:
// bytes 0–7 are the USB setup packet (bmRequestType, bRequest, wValue LE,
// wIndex LE, wLength LE) and bytes 8+ are the data phase. This layout matches
// the Linux usbfs async control transfer convention.
func (h *DeviceHandle) NewControlTransfer(bufferSize int) (*AsyncTransfer, error) {
	if bufferSize < setupPacketSize {
		return nil, ErrInvalidParameter
	}
	return h.newAsyncTransfer(0, TransferTypeControl, bufferSize)
}

func (h *DeviceHandle) newAsyncTransfer(endpoint uint8, tt TransferType, bufferSize int) (*AsyncTransfer, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrDeviceNotFound
	}

	t := &AsyncTransfer{
		handle:       h,
		endpoint:     endpoint,
		transferType: tt,
		buf:          make([]byte, bufferSize),
		timeout:      5 * time.Second,
	}
	t.cond = sync.NewCond(&t.mu)
	return t, nil
}

// SetTimeout overrides the per-transfer timeout. Must be called before Submit.
func (t *AsyncTransfer) SetTimeout(d time.Duration) {
	t.mu.Lock()
	t.timeout = d
	t.mu.Unlock()
}

// Fill copies data into the transfer buffer for OUT transfers. It must be
// called before Submit; for control transfers the first 8 bytes must be the
// USB setup packet.
func (t *AsyncTransfer) Fill(data []byte) error {
	if len(data) > len(t.buf) {
		return fmt.Errorf("fill: data length %d exceeds buffer size %d", len(data), len(t.buf))
	}
	copy(t.buf, data)
	return nil
}

// Buffer returns the filled portion of the transfer buffer, valid after Wait
// returns. It returns an empty slice if no bytes were transferred.
func (t *AsyncTransfer) Buffer() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf[:t.xferred]
}

// ActualLength returns the number of bytes transferred. Valid after Wait.
func (t *AsyncTransfer) ActualLength() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.xferred
}

// Status blocks until the transfer completes and returns its outcome.
func (t *AsyncTransfer) Status() TransferStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.done {
		t.cond.Wait()
	}
	if t.lastErr == ErrTimeout {
		return TransferTimedOut
	}
	if t.lastErr != nil {
		return TransferError
	}
	return TransferCompleted
}

// IsCompleted reports whether the transfer has finished, without blocking.
func (t *AsyncTransfer) IsCompleted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// Submit queues the transfer and returns immediately; it is not complete
// when this returns. Call Wait or WaitWithTimeout.
func (t *AsyncTransfer) Submit() error {
	t.mu.Lock()
	if t.submitted && !t.done {
		t.mu.Unlock()
		return fmt.Errorf("transfer already in flight")
	}
	if t.handle.closed {
		t.mu.Unlock()
		return ErrDeviceNotFound
	}

	t.submitted = true
	t.done = false
	t.xferred = 0
	t.lastErr = nil
	t.overlapped = windows.Overlapped{} // Win32 requires zeroing before reuse.
	timeout := t.timeout
	t.mu.Unlock()

	if t.handle.hid != nil {
		go func() {
			n, err := t.handle.hid.interruptTransfer(t.endpoint, t.buf, timeout)
			t.mu.Lock()
			t.xferred = n
			t.lastErr = err
			t.done = true
			t.submitted = false
			t.cond.Broadcast()
			t.mu.Unlock()
		}()
		return nil
	}

	if err := t.handle.registerAsyncCompletion(&t.overlapped, t); err != nil {
		t.mu.Lock()
		t.submitted = false
		t.mu.Unlock()
		return err
	}

	var submitErr error
	switch t.transferType {
	case TransferTypeBulk, TransferTypeInterrupt:
		submitErr = t.submitPipeIO()
	case TransferTypeControl:
		submitErr = t.submitControlIO()
	default:
		submitErr = ErrNotSupported
	}

	if submitErr != nil {
		t.handle.unregisterAsyncCompletion(&t.overlapped)
		t.mu.Lock()
		t.submitted = false
		t.mu.Unlock()
		return submitErr
	}

	return nil
}

// submitPipeIO issues the real overlapped WinUsb_ReadPipe/WritePipe call
// for a bulk or interrupt transfer. LengthTransferred is deliberately NULL
// -- see this file's package comment.
func (t *AsyncTransfer) submitPipeIO() error {
	h := t.handle
	ifaceHdl := h.getInterfaceHandle(h.interfaceForEndpoint(t.endpoint))

	var dataPtr unsafe.Pointer
	if len(t.buf) > 0 {
		dataPtr = unsafe.Pointer(&t.buf[0])
	}

	var r0 uintptr
	var e1 error
	if t.endpoint&0x80 != 0 {
		r0, _, e1 = syscall.SyscallN(
			procWinUsb_ReadPipe.Addr(),
			uintptr(ifaceHdl),
			uintptr(t.endpoint),
			uintptr(dataPtr),
			uintptr(len(t.buf)),
			0,
			uintptr(unsafe.Pointer(&t.overlapped)),
		)
	} else {
		r0, _, e1 = syscall.SyscallN(
			procWinUsb_WritePipe.Addr(),
			uintptr(ifaceHdl),
			uintptr(t.endpoint),
			uintptr(dataPtr),
			uintptr(len(t.buf)),
			0,
			uintptr(unsafe.Pointer(&t.overlapped)),
		)
	}

	if r0 == 0 && e1 != windows.ERROR_IO_PENDING {
		return fmt.Errorf("submitting async pipe %#x: %w", t.endpoint, e1)
	}
	return nil
}

// submitControlIO issues the real overlapped WinUsb_ControlTransfer call.
// LengthTransferred is deliberately NULL -- see this file's package
// comment. The setup packet is forwarded byte-for-byte from t.buf[:8]
// rather than decoded and re-encoded, since NewControlTransfer's own
// contract already requires it in WinUSB's exact wire layout.
func (t *AsyncTransfer) submitControlIO() error {
	if len(t.buf) < setupPacketSize {
		return ErrInvalidParameter
	}

	var packet [setupPacketSize]byte
	copy(packet[:], t.buf[:setupPacketSize])

	wLength := int(binary.LittleEndian.Uint16(t.buf[6:8]))
	data := t.buf[setupPacketSize:]
	if wLength < len(data) {
		data = data[:wLength]
	}

	ok, err := winusbControlTransfer(t.handle.winusbHandle, packet, data, nil, &t.overlapped)
	if !ok && err != windows.ERROR_IO_PENDING {
		return fmt.Errorf("submitting async control transfer: %w", err)
	}
	return nil
}

// Wait blocks until the transfer completes, honoring the timeout set by
// SetTimeout (5s by default if never called) exactly like WaitWithTimeout
// would with that same value.
func (t *AsyncTransfer) Wait() error {
	return t.WaitWithTimeout(t.timeout)
}

// WaitWithTimeout blocks until the transfer completes or d elapses.
// On timeout, Cancel is called to abort the in-flight pipe.
func (t *AsyncTransfer) WaitWithTimeout(d time.Duration) error {
	done := make(chan error, 1)
	go func() {
		t.mu.Lock()
		for !t.done {
			t.cond.Wait()
		}
		err := t.lastErr
		t.mu.Unlock()
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Cancel() //nolint:errcheck
		return ErrTimeout
	}
}

// Cancel aborts the in-flight transfer. For bulk and interrupt transfers
// this aborts the whole WinUSB pipe (WinUsb_AbortPipe offers no way to
// cancel one specific pending request without affecting others queued on
// the same pipe); for control transfers, which have no pipe of their own,
// CancelIoEx cancels this transfer's own pending overlapped operation
// specifically. Either way the aborted transfer still completes normally
// through iocpPump, now with an error status.
func (t *AsyncTransfer) Cancel() error {
	if t.handle.hid != nil {
		return nil // HID has no overlapped operation here to cancel.
	}

	t.handle.mu.RLock()
	defer t.handle.mu.RUnlock()

	if t.handle.closed {
		return nil
	}

	if t.transferType == TransferTypeControl {
		windows.CancelIoEx(t.handle.fileHandle, &t.overlapped)
		return nil
	}

	if t.handle.winusbHandle == 0 {
		return nil
	}
	ifaceHdl := t.handle.getInterfaceHandle(t.handle.interfaceForEndpoint(t.endpoint))
	syscall.SyscallN(procWinUsb_AbortPipe.Addr(),
		uintptr(ifaceHdl), uintptr(t.endpoint))
	return nil
}

// NewAsyncTransfer creates a new asynchronous transfer.
//
// Deprecated: use DeviceHandle.NewBulkTransfer, NewInterruptTransfer, or
// NewControlTransfer, which report allocation errors.
func NewAsyncTransfer(handle *DeviceHandle, endpoint uint8, transferType TransferType, bufferSize int) *AsyncTransfer {
	t, err := handle.newAsyncTransfer(endpoint, transferType, bufferSize)
	if err != nil {
		return nil
	}
	return t
}
