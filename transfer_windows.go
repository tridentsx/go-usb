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

// Transfer represents a USB transfer on Windows
type Transfer struct {
	handle       *DeviceHandle
	endpoint     uint8
	transferType TransferType
	buffer       []byte
	length       int
	timeout      time.Duration
	callback     TransferCallback
	userdata     interface{}
	status       TransferStatus
	actualLength int
	mu           sync.Mutex
}

// ControlTransfer performs a USB control transfer
func (h *DeviceHandle) ControlTransfer(requestType, request uint8, value, index uint16, data []byte, timeout time.Duration) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	if h.hid != nil {
		return h.hidControlTransfer(requestType, request, value, index, data)
	}

	packet := encodeSetupPacket(requestType, request, value, index, uint16(len(data)))

	// Create overlapped structure for async operation
	var overlapped windows.Overlapped
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateEvent failed: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped.HEvent = event

	var transferred uint32
	ok, e1 := winusbControlTransfer(h.winusbHandle, packet, data, &transferred, &overlapped)
	if !ok {
		if e1 != windows.ERROR_IO_PENDING {
			return 0, fmt.Errorf("WinUsb_ControlTransfer failed: %w", e1)
		}
		return h.awaitOverlapped(event, &overlapped, 0, timeout, false)
	}

	return int(transferred), nil
}

// awaitOverlapped waits for a pending overlapped transfer to finish.
//
// On timeout the pipe is aborted and the completion is then drained with a
// blocking GetOverlappedResult. That second wait matters: until the request
// actually completes, the kernel may still write into the OVERLAPPED structure
// and the caller's data buffer, both of which live on the Go heap.
//
// endpoint is only used to abort a pipe; pass 0 for control transfers, which
// are cancelled with CancelIoEx instead.
func (h *DeviceHandle) awaitOverlapped(
	event windows.Handle,
	overlapped *windows.Overlapped,
	endpoint uint8,
	timeout time.Duration,
	isPipe bool,
) (int, error) {
	timeoutMs := uint32(windows.INFINITE)
	if timeout > 0 {
		timeoutMs = uint32(timeout.Milliseconds())
	}

	waitResult, err := windows.WaitForSingleObject(event, timeoutMs)
	if err != nil {
		return 0, fmt.Errorf("WaitForSingleObject failed: %w", err)
	}

	switch waitResult {
	case uint32(windows.WAIT_OBJECT_0):
		var transferred uint32
		if err := windows.GetOverlappedResult(h.fileHandle, overlapped, &transferred, false); err != nil {
			return 0, err
		}
		return int(transferred), nil

	case uint32(windows.WAIT_TIMEOUT):
		if isPipe {
			syscall.SyscallN(procWinUsb_AbortPipe.Addr(), uintptr(h.winusbHandle), uintptr(endpoint))
		} else {
			windows.CancelIoEx(h.fileHandle, overlapped)
		}
		// Drain the completion so the kernel is finished with the OVERLAPPED
		// and the data buffer before we return and they become reusable.
		var transferred uint32
		windows.GetOverlappedResult(h.fileHandle, overlapped, &transferred, true)
		return 0, ErrTimeout

	default:
		return 0, fmt.Errorf("wait failed: %v", waitResult)
	}
}

// BulkTransfer performs a USB bulk transfer
func (h *DeviceHandle) BulkTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error) {
	return h.BulkTransferWithOptions(endpoint, data, timeout, false)
}

// BulkTransferWithOptions performs a bulk transfer with advanced options
func (h *DeviceHandle) BulkTransferWithOptions(endpoint uint8, data []byte, timeout time.Duration, allowZeroLength bool) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	if len(data) == 0 && !allowZeroLength {
		return 0, ErrInvalidParameter
	}

	if h.hid != nil {
		// A HID device has no bulk endpoints. Interrupt endpoints are reached
		// through InterruptTransfer, which routes to a report.
		return h.hid.interruptTransfer(endpoint, data, timeout)
	}

	// Set timeout for the pipe
	if timeout > 0 {
		ms := uint32(timeout.Milliseconds())
		syscall.SyscallN(
			procWinUsb_SetPipePolicy.Addr(),
			uintptr(h.winusbHandle),
			uintptr(endpoint),
			uintptr(PIPE_TRANSFER_TIMEOUT),
			uintptr(4),
			uintptr(unsafe.Pointer(&ms)),
		)
	}

	var dataPtr unsafe.Pointer
	if len(data) > 0 {
		dataPtr = unsafe.Pointer(&data[0])
	}

	var transferred uint32

	// Create overlapped structure
	var overlapped windows.Overlapped
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateEvent failed: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped.HEvent = event

	// Determine if this is a read or write based on endpoint direction
	isRead := (endpoint & 0x80) != 0

	var r0 uintptr
	var e1 error

	if isRead {
		r0, _, e1 = syscall.SyscallN(
			procWinUsb_ReadPipe.Addr(),
			uintptr(h.winusbHandle),
			uintptr(endpoint),
			uintptr(dataPtr),
			uintptr(len(data)),
			uintptr(unsafe.Pointer(&transferred)),
			uintptr(unsafe.Pointer(&overlapped)),
		)
	} else {
		r0, _, e1 = syscall.SyscallN(
			procWinUsb_WritePipe.Addr(),
			uintptr(h.winusbHandle),
			uintptr(endpoint),
			uintptr(dataPtr),
			uintptr(len(data)),
			uintptr(unsafe.Pointer(&transferred)),
			uintptr(unsafe.Pointer(&overlapped)),
		)
	}

	if r0 == 0 {
		if e1 == windows.ERROR_IO_PENDING {
			return h.awaitOverlapped(event, &overlapped, endpoint, timeout, true)
		}
		return 0, fmt.Errorf("bulk transfer failed: %w", e1)
	}

	return int(transferred), nil
}

// InterruptTransfer performs a USB interrupt transfer
func (h *DeviceHandle) InterruptTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error) {
	return h.InterruptTransferWithRetry(endpoint, data, timeout, 1)
}

// InterruptTransferWithRetry performs interrupt transfer with automatic retry
func (h *DeviceHandle) InterruptTransferWithRetry(endpoint uint8, data []byte, timeout time.Duration, maxRetries int) (int, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		n, err := h.BulkTransfer(endpoint, data, timeout) // Interrupt uses same mechanism as bulk
		if err == nil {
			return n, nil
		}

		lastErr = err

		// Don't retry for certain errors
		if err == ErrDeviceNotFound || err == ErrInvalidParameter {
			break
		}

		// For timeout or I/O errors, try to recover
		if err == ErrTimeout || err == ErrIO {
			if clearErr := h.ClearHalt(endpoint); clearErr != nil {
				break
			}
		}
	}

	return 0, lastErr
}

// ResetEndpoint resets a specific endpoint
func (h *DeviceHandle) ResetEndpoint(endpoint uint8) error {
	return h.ClearHalt(endpoint)
}

// IsochronousTransfer performs an isochronous transfer (not fully supported on Windows WinUSB)
func (h *DeviceHandle) IsochronousTransfer(endpoint uint8, data []byte, numPackets int, packetSize int, timeout time.Duration) ([]IsoPacketResult, error) {
	// WinUSB has limited isochronous support
	return nil, ErrNotSupported
}

// SubmitTransfer submits an async transfer (not implemented)
func (h *DeviceHandle) SubmitTransfer(transfer *Transfer) error {
	return ErrNotSupported
}

// CancelTransfer cancels an async transfer
func (h *DeviceHandle) CancelTransfer(transfer *Transfer) error {
	return ErrNotSupported
}

// ReapTransfer reaps a completed async transfer
func (h *DeviceHandle) ReapTransfer(timeout time.Duration) (*Transfer, error) {
	return nil, ErrNotSupported
}

// NewTransfer creates a new transfer object
func NewTransfer(handle *DeviceHandle, endpoint uint8, transferType TransferType, bufferSize int) *Transfer {
	return &Transfer{
		handle:       handle,
		endpoint:     endpoint,
		transferType: transferType,
		buffer:       make([]byte, bufferSize),
		timeout:      5 * time.Second,
		status:       TransferCompleted,
	}
}

// Transfer methods
func (t *Transfer) SetBuffer(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buffer = data
	t.length = len(data)
}

func (t *Transfer) SetCallback(callback TransferCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = callback
}

func (t *Transfer) SetTimeout(timeout time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = timeout
}

func (t *Transfer) SetUserData(userdata interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.userdata = userdata
}

// GetUserData returns the value previously set with SetUserData.
func (t *Transfer) GetUserData() interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.userdata
}

// Submit queues the transfer on its device handle.
func (t *Transfer) Submit() error {
	if t.handle == nil {
		return ErrInvalidParameter
	}
	return t.handle.SubmitTransfer(t)
}

// Cancel requests cancellation of a previously submitted transfer.
func (t *Transfer) Cancel() error {
	if t.handle == nil {
		return ErrInvalidParameter
	}
	return t.handle.CancelTransfer(t)
}

// Free releases the transfer's buffer.
//
// The transfer must not be in flight. Calling Free more than once is safe.
func (t *Transfer) Free() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buffer = nil
	t.callback = nil
	t.userdata = nil
}

func (t *Transfer) Status() TransferStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Transfer) ActualLength() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.actualLength
}

func (t *Transfer) Buffer() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.actualLength > 0 && t.actualLength <= len(t.buffer) {
		return t.buffer[:t.actualLength]
	}
	return t.buffer
}

// ReadConfigDescriptor reads and parses a configuration descriptor
func (h *DeviceHandle) ReadConfigDescriptor(configIndex uint8) (*ConfigDescriptor, []InterfaceDescriptor, []EndpointDescriptor, error) {
	buf, err := h.RawConfigDescriptor(configIndex)
	if err != nil {
		return nil, nil, nil, err
	}

	if len(buf) < 9 {
		return nil, nil, nil, fmt.Errorf("invalid config descriptor")
	}

	config := &ConfigDescriptor{
		Length:             buf[0],
		DescriptorType:     buf[1],
		TotalLength:        binary.LittleEndian.Uint16(buf[2:4]),
		NumInterfaces:      buf[4],
		ConfigurationValue: buf[5],
		ConfigurationIndex: buf[6],
		Attributes:         buf[7],
		MaxPower:           buf[8],
	}

	interfaces := []InterfaceDescriptor{}
	endpoints := []EndpointDescriptor{}

	pos := int(config.Length)
	for pos < int(config.TotalLength) && pos < len(buf) {
		if pos+2 > len(buf) {
			break
		}

		length := int(buf[pos])
		descType := buf[pos+1]

		if length < 2 || pos+length > len(buf) {
			break
		}

		switch descType {
		case 0x04: // Interface descriptor
			if length >= 9 {
				iface := InterfaceDescriptor{
					Length:            buf[pos],
					DescriptorType:    buf[pos+1],
					InterfaceNumber:   buf[pos+2],
					AlternateSetting:  buf[pos+3],
					NumEndpoints:      buf[pos+4],
					InterfaceClass:    buf[pos+5],
					InterfaceSubClass: buf[pos+6],
					InterfaceProtocol: buf[pos+7],
					InterfaceIndex:    buf[pos+8],
				}
				interfaces = append(interfaces, iface)
			}
		case 0x05: // Endpoint descriptor
			if length >= 7 {
				ep := EndpointDescriptor{
					Length:         buf[pos],
					DescriptorType: buf[pos+1],
					EndpointAddr:   buf[pos+2],
					Attributes:     buf[pos+3],
					MaxPacketSize:  binary.LittleEndian.Uint16(buf[pos+4 : pos+6]),
					Interval:       buf[pos+6],
				}
				endpoints = append(endpoints, ep)
			}
		}

		pos += length
	}

	return config, interfaces, endpoints, nil
}
