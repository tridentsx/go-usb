package usb

import (
	"io"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// usbdIsoPacketDescriptor mirrors USBD_ISO_PACKET_DESCRIPTOR.
// The kernel fills Offset, Length and Status after each isochronous read.
// Write transfers do not receive per-packet results.
type usbdIsoPacketDescriptor struct {
	Offset uint32
	Length uint32
	Status uint32
}

// IsochronousTransfer holds state for a WinUSB isochronous transfer.
//
// Create one with NewIsochronousTransfer; it registers a buffer with the
// WinUSB isochronous API (available on Windows 8.1+). Call Submit to queue
// the transfer, Wait to block for completion, and Close to release the
// WinUSB buffer registration and the Windows event handle.
type IsochronousTransfer struct {
	handle     *DeviceHandle
	ifaceHdl   winusbInterfaceHandle
	endpoint   uint8
	buf        []byte
	isochBuf   uintptr // WINUSB_ISOCH_BUFFER_HANDLE
	overlapped windows.Overlapped
	packets    []usbdIsoPacketDescriptor
	xferred    uint32
	submitted  bool
	done       bool
}

// NewIsochronousTransfer registers a buffer for isochronous transfers on
// endpoint. numPackets and packetSize determine the total buffer size.
//
// Returns ErrNotSupported if the WinUSB isochronous API is not available
// (Windows earlier than 8.1) or the device is not WinUSB-bound.
func (h *DeviceHandle) NewIsochronousTransfer(endpoint uint8, numPackets int, packetSize int) (*IsochronousTransfer, error) {
	if err := procWinUsb_RegisterIsochBuffer.Find(); err != nil {
		return nil, ErrNotSupported
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrDeviceNotFound
	}
	if h.winusbHandle == 0 {
		return nil, ErrNotSupported
	}
	if numPackets <= 0 || packetSize <= 0 {
		return nil, ErrInvalidParameter
	}

	buf := make([]byte, numPackets*packetSize)
	ifaceHdl := h.getInterfaceHandle(h.interfaceForEndpoint(endpoint))

	var isochBuf uintptr
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_RegisterIsochBuffer.Addr(),
		uintptr(ifaceHdl),
		uintptr(endpoint),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&isochBuf)),
	)
	if r0 == 0 {
		return nil, e1
	}

	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		syscall.SyscallN(procWinUsb_UnregisterIsochBuffer.Addr(), isochBuf)
		return nil, err
	}

	t := &IsochronousTransfer{
		handle:   h,
		ifaceHdl: ifaceHdl,
		endpoint: endpoint,
		buf:      buf,
		isochBuf: isochBuf,
		packets:  make([]usbdIsoPacketDescriptor, numPackets),
	}
	t.overlapped.HEvent = event
	return t, nil
}

// Submit queues the isochronous transfer. For IN endpoints the buffer is
// filled by the host controller; for OUT endpoints the caller should fill
// Buffer() before calling Submit.
func (t *IsochronousTransfer) Submit() error {
	if t.submitted && !t.done {
		return ErrInvalidParameter
	}
	windows.ResetEvent(t.overlapped.HEvent)
	t.submitted = true
	t.done = false
	t.xferred = 0

	if t.endpoint&0x80 != 0 {
		r0, _, e1 := syscall.SyscallN(
			procWinUsb_ReadIsochPipeAsap.Addr(),
			t.isochBuf,
			0,                                      // Offset
			uintptr(len(t.buf)),                    // Length
			0,                                      // ContinueStream = FALSE
			uintptr(len(t.packets)),
			uintptr(unsafe.Pointer(&t.packets[0])),
			uintptr(unsafe.Pointer(&t.overlapped)),
		)
		if r0 == 0 && e1 != windows.ERROR_IO_PENDING {
			return e1
		}
	} else {
		r0, _, e1 := syscall.SyscallN(
			procWinUsb_WriteIsochPipeAsap.Addr(),
			t.isochBuf,
			0,                                      // Offset
			uintptr(len(t.buf)),                    // Length
			0,                                      // ContinueStream = FALSE
			uintptr(unsafe.Pointer(&t.overlapped)),
		)
		if r0 == 0 && e1 != windows.ERROR_IO_PENDING {
			return e1
		}
	}
	return nil
}

// Wait blocks until the submitted transfer completes.
func (t *IsochronousTransfer) Wait() error {
	if !t.submitted || t.done {
		return ErrInvalidParameter
	}
	r, err := windows.WaitForSingleObject(t.overlapped.HEvent, windows.INFINITE)
	if r != windows.WAIT_OBJECT_0 {
		return err
	}
	err = windows.GetOverlappedResult(t.handle.fileHandle, &t.overlapped, &t.xferred, false)
	t.done = true
	return err
}

// Cancel aborts an in-progress transfer by aborting the pipe.
func (t *IsochronousTransfer) Cancel() error {
	if !t.submitted || t.done {
		return nil
	}
	syscall.SyscallN(procWinUsb_AbortPipe.Addr(),
		uintptr(t.ifaceHdl), uintptr(t.endpoint))
	return nil
}

// Status returns TransferCompleted once Wait has returned, TransferError otherwise.
func (t *IsochronousTransfer) Status() TransferStatus {
	if t.done {
		return TransferCompleted
	}
	return TransferError
}

// ActualLength returns the total bytes transferred, valid after Wait.
func (t *IsochronousTransfer) ActualLength() int { return int(t.xferred) }

// Buffer returns the underlying data buffer.
func (t *IsochronousTransfer) Buffer() []byte { return t.buf }

// Packets returns per-packet descriptors as filled by the kernel after a
// completed IN transfer. Write transfers return zeroed descriptors.
func (t *IsochronousTransfer) Packets() []IsoPacketDescriptor {
	result := make([]IsoPacketDescriptor, len(t.packets))
	for i, p := range t.packets {
		result[i] = IsoPacketDescriptor{
			Length:       p.Length,
			ActualLength: p.Length,
			Status:       int32(p.Status),
		}
	}
	return result
}

// IsoPacketBuffer returns the slice of Buffer belonging to packet packetIndex.
//
// The byte offset of each packet is computed from the fixed packet size
// (total buffer / packet count) rather than from USBD_ISO_PACKET_DESCRIPTOR.Offset:
// per Microsoft's own WinUsb_ReadIsochPipe/ReadIsochPipeAsap docs, the
// IsoPacketDescriptors array is [out]-only and documented to receive just the
// status and size of each packet after completion -- Offset is never
// documented as being written back, so it cannot be trusted to locate a
// packet's data. After a completed IN transfer p.Length contains the actual
// bytes received.
func (t *IsochronousTransfer) IsoPacketBuffer(packetIndex int) ([]byte, error) {
	if packetIndex < 0 || packetIndex >= len(t.packets) {
		return nil, ErrInvalidParameter
	}
	packetSize := len(t.buf) / len(t.packets)
	start := packetIndex * packetSize
	end := min(start+int(t.packets[packetIndex].Length), start+packetSize)
	return t.buf[start:end], nil
}

// IsoPacketBufferSlices returns a slice-per-packet view of the buffer.
func (t *IsochronousTransfer) IsoPacketBufferSlices() [][]byte {
	slices := make([][]byte, len(t.packets))
	packetSize := len(t.buf) / len(t.packets)
	for i, p := range t.packets {
		start := i * packetSize
		slices[i] = t.buf[start:min(start+int(p.Length), start+packetSize)]
	}
	return slices
}

// Close releases the WinUSB isochronous buffer registration and event handle.
// It must be called when the transfer is no longer needed.
func (t *IsochronousTransfer) Close() error {
	if t.isochBuf != 0 {
		syscall.SyscallN(procWinUsb_UnregisterIsochBuffer.Addr(), t.isochBuf)
		t.isochBuf = 0
	}
	if t.overlapped.HEvent != 0 {
		windows.CloseHandle(t.overlapped.HEvent)
		t.overlapped.HEvent = 0
	}
	return nil
}

// AsyncBulkTransfer represents an asynchronous bulk USB transfer backed by a
// goroutine that runs a blocking overlapped BulkTransfer.
type AsyncBulkTransfer struct {
	handle     *DeviceHandle
	endpoint   uint8
	bufferSize int
	buffer     []byte
	result     []byte
	resultErr  error
	submitted  bool
	completed  bool
	closed     bool
	mu         sync.Mutex
	cond       *sync.Cond
}

// NewAsyncBulkTransfer creates a new async bulk transfer.
func (h *DeviceHandle) NewAsyncBulkTransfer(endpoint uint8, bufferSize int) (*AsyncBulkTransfer, error) {
	if h.closed {
		return nil, ErrDeviceNotFound
	}
	t := &AsyncBulkTransfer{
		handle:     h,
		endpoint:   endpoint,
		bufferSize: bufferSize,
		buffer:     make([]byte, bufferSize),
	}
	t.cond = sync.NewCond(&t.mu)
	return t, nil
}

// Submit starts the bulk transfer in a background goroutine.
func (t *AsyncBulkTransfer) Submit() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return io.EOF
	}
	if t.submitted && !t.completed {
		return ErrInvalidParameter
	}

	t.submitted = true
	t.completed = false
	t.result = nil
	t.resultErr = nil

	go func() {
		n, err := t.handle.BulkTransfer(t.endpoint, t.buffer, 5*time.Second)
		t.mu.Lock()
		if err != nil {
			t.resultErr = err
		} else {
			t.result = make([]byte, n)
			copy(t.result, t.buffer[:n])
		}
		t.completed = true
		t.cond.Broadcast()
		t.mu.Unlock()
	}()
	return nil
}

// Wait blocks until the transfer completes and returns the received data.
func (t *AsyncBulkTransfer) Wait() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for !t.completed && !t.closed {
		t.cond.Wait()
	}
	if t.closed {
		return nil, io.EOF
	}
	return t.result, t.resultErr
}

// Cancel aborts the in-progress transfer via WinUsb_AbortPipe.
func (t *AsyncBulkTransfer) Cancel() error {
	t.handle.mu.RLock()
	defer t.handle.mu.RUnlock()
	if !t.handle.closed {
		syscall.SyscallN(procWinUsb_AbortPipe.Addr(),
			uintptr(t.handle.winusbHandle), uintptr(t.endpoint))
	}
	return nil
}

// ActualLength returns the number of bytes transferred.
func (t *AsyncBulkTransfer) ActualLength() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.result)
}

// Read performs a synchronous bulk read into buf.
func (t *AsyncBulkTransfer) Read(buf []byte) (int, error) {
	if t.closed {
		return 0, io.EOF
	}
	if t.handle.closed {
		return 0, ErrDeviceNotFound
	}
	return t.handle.BulkTransfer(t.endpoint, buf, 5*time.Second)
}

// Close closes the async bulk transfer.
func (t *AsyncBulkTransfer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.cond.Broadcast()
	return nil
}
