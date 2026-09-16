//go:build darwin && !cgo

// Transfer types and device-handle operations for the CGO-free macOS backend.
//
// Stage 1 implements enumeration only. Everything that needs an open device
// reports ErrNotSupported, because opening one requires calling methods on
// IOKit's COM-style interfaces via vtable dispatch, which is the next stage.
// Nothing here returns a nil error to pretend an operation happened.
//
// The types exist with the same fields and signatures as the cgo backend so that
// transfer_darwin.go and compat_darwin.go compile unchanged in both
// configurations, and so the contract assertions in api_contract.go hold.
//
// See issue #14.

package usb

import (
	"sync"
	"time"
)

// --- IOKit COM-style interfaces ----------------------------------------------

// ControlTransfer performs a control transfer on the default control endpoint.
func (d *IOUSBDeviceInterface) ControlTransfer(bmRequestType, bRequest uint8, wValue, wIndex uint16, data []byte, timeout uint32) (int, error) {
	return 0, ErrNotSupported
}

// BulkTransferIn reads from a bulk or interrupt pipe.
func (i *IOUSBInterfaceInterface) BulkTransferIn(pipeRef uint8, data []byte, timeout uint32) (int, error) {
	return 0, ErrNotSupported
}

// BulkTransferOut writes to a bulk or interrupt pipe.
func (i *IOUSBInterfaceInterface) BulkTransferOut(pipeRef uint8, data []byte, timeout uint32) (int, error) {
	return 0, ErrNotSupported
}

// --- DeviceHandle -------------------------------------------------------------

// Close closes the device handle.
func (h *DeviceHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil
	}
	h.closed = true
	h.interfaces = nil
	h.claimedIfaces = nil
	return nil
}

// SetConfiguration selects a device configuration.
func (h *DeviceHandle) SetConfiguration(config int) error { return ErrNotSupported }

// GetConfiguration returns the active configuration value.
func (h *DeviceHandle) GetConfiguration() (int, error) { return 0, ErrNotSupported }

// ClaimInterface claims an interface for I/O.
func (h *DeviceHandle) ClaimInterface(iface uint8) error { return ErrNotSupported }

// ReleaseInterface releases a claimed interface.
func (h *DeviceHandle) ReleaseInterface(iface uint8) error { return ErrNotSupported }

// SetAltSetting selects an alternate setting on an interface.
func (h *DeviceHandle) SetAltSetting(iface, altSetting uint8) error { return ErrNotSupported }

// ClearHalt clears a stall condition on an endpoint.
func (h *DeviceHandle) ClearHalt(endpoint uint8) error { return ErrNotSupported }

// ResetDevice resets the device.
func (h *DeviceHandle) ResetDevice() error { return ErrNotSupported }

// ResetEndpoint resets an endpoint.
func (h *DeviceHandle) ResetEndpoint(endpoint uint8) error { return ErrNotSupported }

// KernelDriverActive reports whether a kernel driver holds the interface.
//
// IOKit does not expose driver bindings the way Linux does; a claimed interface
// is the only signal, and it surfaces as ClaimInterface failing.
func (h *DeviceHandle) KernelDriverActive(iface uint8) (bool, error) {
	return false, ErrNotSupported
}

// DetachKernelDriver detaches the kernel driver from an interface.
//
// macOS provides no user-space way to unbind a kernel driver, so this always
// reports ErrNotSupported rather than pretending to have detached anything.
func (h *DeviceHandle) DetachKernelDriver(iface uint8) error { return ErrNotSupported }

// AttachKernelDriver re-attaches the kernel driver to an interface.
//
// macOS provides no user-space way to rebind a kernel driver.
func (h *DeviceHandle) AttachKernelDriver(iface uint8) error { return ErrNotSupported }

// StringDescriptor reads a string descriptor.
//
// Enumeration caches the manufacturer, product and serial strings from the IOKit
// registry, so Device.SysfsStrings carries them without an open device.
func (h *DeviceHandle) StringDescriptor(index uint8) (string, error) {
	return "", ErrNotSupported
}

// GetDeviceDescriptor returns the device descriptor.
func (h *DeviceHandle) GetDeviceDescriptor() (*DeviceDescriptor, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || h.device == nil {
		return nil, ErrDeviceNotFound
	}
	desc := h.device.Descriptor
	return &desc, nil
}

// GetActiveConfigDescriptor returns the descriptor for the active configuration.
func (h *DeviceHandle) GetActiveConfigDescriptor() (*ConfigDescriptor, error) {
	return nil, ErrNotSupported
}

// GetConfigDescriptor returns a configuration descriptor by index.
func (h *DeviceHandle) GetConfigDescriptor(index uint8) (*ConfigDescriptor, error) {
	return nil, ErrNotSupported
}

// GetBOSDescriptor reads the Binary Object Store descriptor.
func (h *DeviceHandle) GetBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error) {
	return nil, nil, ErrNotSupported
}

// GetDeviceQualifierDescriptor reads the device qualifier descriptor.
func (h *DeviceHandle) GetDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error) {
	return nil, ErrNotSupported
}

// GetCapabilities returns platform capability bits, which IOKit does not expose.
func (h *DeviceHandle) GetCapabilities() (uint32, error) { return 0, ErrNotSupported }

// GetSpeed returns the device's negotiated speed.
func (h *DeviceHandle) GetSpeed() (Speed, error) { return SpeedUnknown, ErrNotSupported }

// --- AsyncTransfer ------------------------------------------------------------

// AsyncTransfer represents an asynchronous USB transfer on macOS.
type AsyncTransfer struct {
	*Transfer
	handle    *DeviceHandle
	submitted bool
	completed bool
	mutex     sync.Mutex
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
	}
}

// Submit queues the transfer.
func (t *AsyncTransfer) Submit() error { return ErrNotSupported }

// Cancel requests cancellation of a submitted transfer.
func (t *AsyncTransfer) Cancel() error { return ErrNotSupported }

// IsCompleted reports whether the transfer has completed. It never blocks.
func (t *AsyncTransfer) IsCompleted() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.completed
}

// AsyncBulkTransfer submits a bulk transfer and reports completion via callback.
func (h *DeviceHandle) AsyncBulkTransfer(endpoint uint8, data []byte, callback func(*Transfer)) error {
	return ErrNotSupported
}

// AsyncInterruptTransfer submits an interrupt transfer and reports completion
// via callback.
func (h *DeviceHandle) AsyncInterruptTransfer(endpoint uint8, data []byte, callback func(*Transfer)) error {
	return ErrNotSupported
}

// HandleEvents services pending asynchronous transfer completions.
//
// IOKit delivers completions on a CFRunLoop. Until asynchronous transfers are
// implemented there is nothing to pump, so this returns nil to keep portable
// code that calls it unconditionally working.
func HandleEvents(timeout time.Duration) error { return nil }

// RunEventLoop services asynchronous completions until stop is closed.
func RunEventLoop(stop <-chan struct{}) { <-stop }

// --- IsochronousTransfer ------------------------------------------------------

// IsochronousTransfer represents an isochronous USB transfer.
type IsochronousTransfer struct {
	handle         *DeviceHandle
	endpoint       uint8
	packetSize     int
	numPackets     int
	buffer         []byte
	status         TransferStatus
	actualLength   int
	callback       func(*IsochronousTransfer)
	userData       interface{}
	submitted      bool
	completed      bool
	mutex          sync.Mutex
	packetLengths  []int
	packetStatuses []int
}

// NewIsochronousTransfer creates a new isochronous transfer.
//
// Deprecated: use DeviceHandle.NewIsochronousTransfer, which is the form used on
// every platform and reports errors.
func NewIsochronousTransfer(handle *DeviceHandle, endpoint uint8, numPackets int, packetSize int) *IsochronousTransfer {
	return &IsochronousTransfer{
		handle:         handle,
		endpoint:       endpoint,
		packetSize:     packetSize,
		numPackets:     numPackets,
		buffer:         make([]byte, numPackets*packetSize),
		packetLengths:  make([]int, numPackets),
		packetStatuses: make([]int, numPackets),
	}
}

// SetCallback sets the completion callback.
func (t *IsochronousTransfer) SetCallback(callback func(*IsochronousTransfer)) {
	t.callback = callback
}

// SetUserData attaches caller data to the transfer.
func (t *IsochronousTransfer) SetUserData(data interface{}) { t.userData = data }

// GetUserData returns the data attached with SetUserData.
func (t *IsochronousTransfer) GetUserData() interface{} { return t.userData }

// SetPacketLength sets the requested length of one packet.
func (t *IsochronousTransfer) SetPacketLength(packet int, length int) error {
	if packet < 0 || packet >= t.numPackets {
		return ErrInvalidParameter
	}
	if length < 0 || length > t.packetSize {
		return ErrInvalidParameter
	}
	t.packetLengths[packet] = length
	return nil
}

// Submit queues the transfer.
func (t *IsochronousTransfer) Submit() error { return ErrNotSupported }

// Cancel requests cancellation of a submitted transfer.
func (t *IsochronousTransfer) Cancel() error { return ErrNotSupported }

// Wait blocks until the transfer completes.
func (t *IsochronousTransfer) Wait() error { return ErrNotSupported }

// Status returns the transfer status.
func (t *IsochronousTransfer) Status() TransferStatus { return t.status }

// ActualLength returns the total bytes transferred.
func (t *IsochronousTransfer) ActualLength() int { return t.actualLength }

// GetPacketData returns the data for a specific packet.
//
// Deprecated: use IsoPacketBuffer.
func (t *IsochronousTransfer) GetPacketData(packet int) ([]byte, error) {
	return t.IsoPacketBuffer(packet)
}

// GetPacketStatus returns the status of a specific packet.
//
// Deprecated: use Packets and read IsoPacketDescriptor.Status.
func (t *IsochronousTransfer) GetPacketStatus(packet int) (int, error) {
	if packet < 0 || packet >= t.numPackets {
		return 0, ErrInvalidParameter
	}
	return t.packetStatuses[packet], nil
}

// GetPacketActualLength returns the actual length transferred for a packet.
//
// Deprecated: use Packets and read IsoPacketDescriptor.ActualLength.
func (t *IsochronousTransfer) GetPacketActualLength(packet int) (int, error) {
	if packet < 0 || packet >= t.numPackets {
		return 0, ErrInvalidParameter
	}
	return t.packetLengths[packet], nil
}

// IsochronousTransferIn creates an isochronous transfer for reading.
func (h *DeviceHandle) IsochronousTransferIn(endpoint uint8, numPackets, packetSize int) (*IsochronousTransfer, error) {
	return nil, ErrNotSupported
}

// IsochronousTransferOut creates an isochronous transfer for writing.
func (h *DeviceHandle) IsochronousTransferOut(endpoint uint8, data []byte, numPackets, packetSize int) (*IsochronousTransfer, error) {
	return nil, ErrNotSupported
}
