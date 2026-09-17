package usb

import "time"

// This file defines the canonical, cross-platform API surface of the package
// and asserts at compile time that every backend implements it.
//
// The interfaces here are documentation with teeth: adding a method to one
// backend without adding it to the others, or changing a signature on a single
// platform, breaks the build on every platform rather than silently producing
// an API that only compiles on the maintainer's laptop.
//
// Membership rule: a method belongs here when it is meaningful on every
// platform. Operations a platform cannot perform must still be present and
// must return ErrNotSupported -- never a nil error that pretends the operation
// happened. Genuinely platform-specific escape hatches are deliberately kept
// out; see the notes at the bottom of this file.

// DeviceHandleInterface is the portable contract for an open USB device.
//
// Every platform backend implements this in full.
type DeviceHandleInterface interface {
	// Lifecycle and identity.
	Close() error
	Device() *Device
	Descriptor() DeviceDescriptor

	// Configuration.
	SetConfiguration(config int) error
	GetConfiguration() (int, error)
	Configuration() (int, error)

	// Interfaces and alternate settings.
	ClaimInterface(iface uint8) error
	ReleaseInterface(iface uint8) error
	SetAltSetting(iface, altSetting uint8) error
	SetInterfaceAltSetting(iface uint8, altSetting uint8) error
	Interface(iface uint8) (uint8, error)

	// Endpoint and device state.
	ClearHalt(endpoint uint8) error
	ResetDevice() error
	ResetEndpoint(endpoint uint8) error

	// Kernel driver interaction. Only Linux can actually unbind a driver; the
	// other backends report ErrNotSupported.
	KernelDriverActive(iface uint8) (bool, error)
	DetachKernelDriver(iface uint8) error
	AttachKernelDriver(iface uint8) error

	// Synchronous transfers.
	ControlTransfer(requestType, request uint8, value, index uint16, data []byte, timeout time.Duration) (int, error)
	BulkTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error)
	BulkTransferWithOptions(endpoint uint8, data []byte, timeout time.Duration, allowZeroLength bool) (int, error)
	InterruptTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error)
	InterruptTransferWithRetry(endpoint uint8, data []byte, timeout time.Duration, maxRetries int) (int, error)
	IsochronousTransfer(endpoint uint8, data []byte, numPackets int, packetSize int, timeout time.Duration) ([]IsoPacketResult, error)

	// Descriptors.
	StringDescriptor(index uint8) (string, error)
	RawDescriptor(descType uint8, descIndex uint8, langID uint16, data []byte) (int, error)
	SetDescriptor(descType uint8, descIndex uint8, langID uint16, data []byte) error
	RawConfigDescriptor(index uint8) ([]byte, error)
	ConfigDescriptorByValue(index uint8) (*ConfigDescriptor, error)
	ReadConfigDescriptor(configIndex uint8) (*ConfigDescriptor, []InterfaceDescriptor, []EndpointDescriptor, error)
	GetConfigDescriptor(index uint8) (*ConfigDescriptor, error)
	GetActiveConfigDescriptor() (*ConfigDescriptor, error)
	GetDeviceDescriptor() (*DeviceDescriptor, error)
	GetBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error)
	ReadBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error)
	GetDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error)
	ReadDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error)
	USB20ExtensionDescriptor() (*USB2ExtensionCapability, error)
	SSUSBDeviceCapabilityDescriptor() (*SuperSpeedUSBCapability, error)
	SSEndpointCompanionDescriptor(configIndex uint8, interfaceNumber uint8, altSetting uint8, endpointAddress uint8) (*SuperSpeedEndpointCompanionDescriptor, error)

	// Standard requests.
	Status(requestType uint8, index uint16) (uint16, error)
	GetStatus(recipient, index uint16) (uint16, error)
	SetFeature(requestType uint8, feature uint16, index uint16) error
	ClearFeature(requestType uint8, feature uint16, index uint16) error
	SynchFrame(endpoint uint8) (uint16, error)

	// Capabilities and speed.
	Speed() (uint8, error)
	GetSpeed() (Speed, error)
	Capabilities() (uint32, error)
	GetCapabilities() (uint32, error)

	// Bulk streams (USB 3.0). Only Linux implements these today.
	AllocStreams(numStreams uint32, endpoints []uint8) error
	FreeStreams(endpoints []uint8) error

	// Transfer objects.
	SubmitTransfer(transfer *Transfer) error
	CancelTransfer(transfer *Transfer) error
	ReapTransfer(timeout time.Duration) (*Transfer, error)
	NewIsochronousTransfer(endpoint uint8, numPackets int, packetSize int) (*IsochronousTransfer, error)
}

// TransferInterface is the portable contract for a Transfer object.
type TransferInterface interface {
	SetBuffer(data []byte)
	SetCallback(callback TransferCallback)
	SetTimeout(timeout time.Duration)
	SetUserData(userdata interface{})
	GetUserData() interface{}
	Status() TransferStatus
	ActualLength() int
	Buffer() []byte
	Submit() error
	Cancel() error
	Free()
}

// Compile-time conformance assertions. These are the enforcement mechanism;
// they cost nothing at runtime.
var (
	_ DeviceHandleInterface = (*DeviceHandle)(nil)
	_ TransferInterface     = (*Transfer)(nil)
)

// AsyncTransferInterface is the portable contract for an asynchronous
// bulk, interrupt, or control transfer.
type AsyncTransferInterface interface {
	Submit() error
	Wait() error
	WaitWithTimeout(timeout time.Duration) error
	Cancel() error
	IsCompleted() bool
	Status() TransferStatus
	ActualLength() int
	Buffer() []byte
	SetTimeout(timeout time.Duration)
	Fill(data []byte) error
}

// IsochronousTransferInterface is the portable contract for an isochronous
// transfer.
type IsochronousTransferInterface interface {
	Submit() error
	Wait() error
	Cancel() error
	Status() TransferStatus
	ActualLength() int
	Buffer() []byte
	Packets() []IsoPacketDescriptor
	IsoPacketBuffer(packetIndex int) ([]byte, error)
	IsoPacketBufferSlices() [][]byte
}

// Deliberately excluded from the portable contract:
//
//   - Linux: Fd, Wrapped, WrapSysDevice. These expose a usbfs file descriptor,
//     which has no counterpart elsewhere.
//   - macOS: AsyncBulkTransfer, AsyncInterruptTransfer, IsochronousTransferIn,
//     IsochronousTransferOut, HandleEvents, RunEventLoop. These are shaped by
//     CFRunLoop and are retained as macOS conveniences.
//   - Windows: SetPipePolicy, SetTimeout. These configure WinUSB pipe policy,
//     which is a WinUSB concept.
//
// AsyncTransfer and IsochronousTransfer are asserted against their interfaces
// in api_contract_async.go, which covers all three platforms.
