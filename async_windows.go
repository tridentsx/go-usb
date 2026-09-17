package usb

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
)

// AsyncTransfer is the Windows implementation of asynchronous USB transfers.
//
// Internally Submit fires a goroutine that calls the synchronous WinUSB pipe
// API so that the caller's goroutine is never blocked. Cancel aborts the
// in-flight WinUSB pipe, which unblocks the transfer goroutine. The type
// satisfies AsyncTransferInterface (asserted in api_contract_async.go).
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

// Submit queues the transfer. The actual I/O runs on a goroutine owned by this
// package; call Wait or WaitWithTimeout to collect the result.
func (t *AsyncTransfer) Submit() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.submitted && !t.done {
		return fmt.Errorf("transfer already in flight")
	}
	if t.handle.closed {
		return ErrDeviceNotFound
	}

	t.submitted = true
	t.done = false
	t.xferred = 0
	t.lastErr = nil

	timeout := t.timeout

	go func() {
		var n int
		var err error

		switch t.transferType {
		case TransferTypeBulk, TransferTypeInterrupt:
			n, err = t.handle.BulkTransfer(t.endpoint, t.buf, timeout)

		case TransferTypeControl:
			if len(t.buf) < setupPacketSize {
				err = ErrInvalidParameter
				break
			}
			requestType := t.buf[0]
			request := t.buf[1]
			value := binary.LittleEndian.Uint16(t.buf[2:4])
			index := binary.LittleEndian.Uint16(t.buf[4:6])
			wLength := int(binary.LittleEndian.Uint16(t.buf[6:8]))
			data := t.buf[setupPacketSize:]
			if wLength < len(data) {
				data = data[:wLength]
			}
			n, err = t.handle.ControlTransfer(requestType, request, value, index, data, timeout)

		default:
			err = ErrNotSupported
		}

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

// Wait blocks until the transfer completes and returns any error.
func (t *AsyncTransfer) Wait() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.done {
		t.cond.Wait()
	}
	return t.lastErr
}

// WaitWithTimeout blocks until the transfer completes or d elapses.
// On timeout, Cancel is called to abort the in-flight pipe.
func (t *AsyncTransfer) WaitWithTimeout(d time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- t.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Cancel() //nolint:errcheck
		return ErrTimeout
	}
}

// Cancel aborts the in-flight transfer by aborting its WinUSB pipe. For
// control transfers, AbortPipe is not available; Cancel is a no-op and the
// transfer completes normally (or with an I/O error on device removal).
func (t *AsyncTransfer) Cancel() error {
	t.handle.mu.RLock()
	defer t.handle.mu.RUnlock()

	if t.handle.closed || t.transferType == TransferTypeControl {
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
