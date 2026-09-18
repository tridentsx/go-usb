// Device and handle types for the macOS backend, reached through purego
// rather than cgo (see #14).
//
// Field names and method signatures are what transfer_darwin.go and
// compat_darwin.go, shared across every macOS build, expect.

package usb

import (
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// IOKitDevice identifies a USB device in the IOKit registry.
type IOKitDevice struct {
	// Service is the io_service_t for the registry entry. It is only valid
	// during enumeration; the entry is released afterwards.
	Service    uint32
	LocationID uint32
	VendorID   uint16
	ProductID  uint16
	Bus        uint8
	Address    uint8
}

// Device represents a USB device on macOS.
type Device struct {
	Path    string
	Bus     uint8
	Address uint8

	// Port and ParentHubAddr stay zero on macOS: IOKit topology traversal
	// to populate them (matching Linux's sysfs parsing and Windows' hub
	// devnode walk) is future work, not yet needed by anything here. They
	// exist so cmd/lsusb's cross-platform tree view compiles and degrades
	// to a flat listing on this platform instead of failing to build.
	Port          uint8
	Speed         Speed
	ParentHubAddr uint8

	Descriptor  DeviceDescriptor
	IOKitDevice *IOKitDevice

	// Configs holds the raw configuration descriptor headers. Reading them
	// requires opening the device, so it is empty until that is implemented.
	Configs []RawConfigDescriptor

	// SysfsStrings holds the string descriptors cached during enumeration. The
	// name is shared with the other platforms; see DeviceStrings.
	SysfsStrings *SysfsStrings

	// CachedStrings is the historical macOS-only name for SysfsStrings and
	// points at the same value.
	//
	// Deprecated: use SysfsStrings, which exists on every platform.
	CachedStrings *CachedStrings
}

// CachedStrings is the historical macOS-only name for DeviceStrings.
//
// Deprecated: use DeviceStrings instead.
type CachedStrings = DeviceStrings

// IOUSBDeviceInterface wraps IOKit's IOUSBDeviceInterface, a COM-style
// structure of function pointers.
//
// handle is what IOCreatePlugInInterfaceForService plus QueryInterface yields: a
// pointer to a pointer to the method table. Methods are dispatched through it in
// device_interface_darwin.go.
type IOUSBDeviceInterface struct {
	handle unsafe.Pointer
}

// IOUSBInterfaceInterface wraps IOKit's IOUSBInterfaceInterface.
type IOUSBInterfaceInterface struct {
	handle unsafe.Pointer

	// The fields below back the async event pump isochronous transfers need.
	// It is started lazily, once, on first use, and stopped when the
	// interface is released; see ensureAsyncPump and stopAsyncPump in
	// isochronous_darwin.go.
	asyncMu      sync.Mutex
	asyncStarted bool
	asyncErr     error
	runLoop      uintptr
	asyncMode    uintptr // a CFString for kCFRunLoopDefaultMode, released at stop
	asyncStopped bool
	asyncReady   chan struct{}
	asyncDone    chan struct{}

	// callback is one IOAsyncCallback1 trampoline, reused for every
	// isochronous transfer on this interface: purego.NewCallback's allocation
	// is never freed, so creating one per Submit would exhaust its pool
	// under any real workload. pending correlates a completion back to its
	// transfer via arg0, which IOKit documents as the frameList pointer the
	// read/write call was given.
	callback  uintptr
	pendingMu sync.Mutex
	pending   map[uintptr]*IsochronousTransfer

	// bulkCallback is the same idea for ReadPipeAsync/WritePipeAsync (bulk and
	// interrupt), which share this interface's async pump but need their own
	// trampoline: their arg0 is documented as the byte count, not a pointer,
	// so completions are correlated via refcon (an address of the transfer's
	// own buffer) instead of arg0.
	bulkCallback  uintptr
	pendingBulkMu sync.Mutex
	pendingBulk   map[uintptr]*AsyncTransfer
}

// DeviceHandle represents an open USB device on macOS.
type DeviceHandle struct {
	device        *Device
	devInterface  *IOUSBDeviceInterface
	interfaces    map[uint8]*IOUSBInterfaceInterface
	claimedIfaces map[uint8]bool
	mu            sync.RWMutex
	closed        bool

	// hid is non-nil when a claimed interface is owned by IOUSBHIDDriver and
	// reached through the IOHIDDevice transport in hid_darwin.go rather than a
	// normal IOUSBInterfaceInterface. See ClaimInterface's fallback.
	hid *hidDevice
}

// IOKitEnumerator handles USB device enumeration via IOKit.
type IOKitEnumerator struct{}

// NewIOKitEnumerator creates a new IOKit enumerator.
func NewIOKitEnumerator() *IOKitEnumerator {
	return &IOKitEnumerator{}
}

// EnumerateDevices returns all USB devices found via IOKit.
func (e *IOKitEnumerator) EnumerateDevices() ([]*Device, error) {
	return enumerateDevices()
}

// DeviceListOption is a functional option for configuring DeviceList behavior.
type DeviceListOption func(*deviceListOptions)

// deviceListOptions holds the configuration for DeviceList.
type deviceListOptions struct {
	includeInaccessible bool
}

// WithInaccessibleDevices returns an option that includes devices that cannot
// be opened. On macOS every device is described from the IOKit registry, so
// this option has no effect and is provided for API compatibility.
func WithInaccessibleDevices() DeviceListOption {
	return func(o *deviceListOptions) {
		o.includeInaccessible = true
	}
}

// DeviceList returns a list of all USB devices on the system.
//
// Devices are described from the IOKit registry, so this works whichever driver
// owns a device. A system with no USB devices yields an empty list and a nil
// error.
func DeviceList(opts ...DeviceListOption) ([]*Device, error) {
	options := &deviceListOptions{}
	for _, opt := range opts {
		opt(options)
	}

	devices, err := enumerateDevices()
	if err != nil {
		return nil, err
	}
	if devices == nil {
		devices = []*Device{}
	}
	return devices, nil
}

// Open opens the USB device.
//
// The IOKit service is looked up again by locationID, because enumeration
// releases the registry entries it walked. A device interface is then obtained
// and opened for exclusive access.
func (d *Device) Open() (*DeviceHandle, error) {
	if d == nil || d.IOKitDevice == nil {
		return nil, ErrInvalidParameter
	}

	service, err := serviceForLocationID(d.IOKitDevice.LocationID)
	if err != nil {
		return nil, err
	}
	defer releaseService(service)

	iface, err := openDeviceInterface(service)
	if err != nil {
		return nil, err
	}

	if err := iface.open(); err != nil {
		iface.release()
		return nil, err
	}

	return &DeviceHandle{
		device:        d,
		devInterface:  iface,
		interfaces:    make(map[uint8]*IOUSBInterfaceInterface),
		claimedIfaces: make(map[uint8]bool),
	}, nil
}

// OpenDevice opens a USB device by vendor and product ID.
func OpenDevice(vendorID, productID uint16) (*DeviceHandle, error) {
	devices, err := DeviceList()
	if err != nil {
		return nil, err
	}

	for _, dev := range devices {
		if dev.Descriptor.VendorID == vendorID && dev.Descriptor.ProductID == productID {
			return dev.Open()
		}
	}
	return nil, ErrDeviceNotFound
}

// OpenDeviceWithPath opens the USB device at the given IOKit path, for example
// "iokit:14200000".
func OpenDeviceWithPath(path string) (*DeviceHandle, error) {
	if !IsValidDevicePath(path) {
		return nil, ErrInvalidParameter
	}

	devices, err := DeviceList()
	if err != nil {
		return nil, err
	}

	for _, dev := range devices {
		if dev.Path == path {
			return dev.Open()
		}
	}
	return nil, ErrDeviceNotFound
}

// IsValidDevicePath checks if the given path is a valid USB device path.
func IsValidDevicePath(path string) bool {
	if !strings.HasPrefix(path, "iokit:") {
		return false
	}

	locationStr := strings.TrimPrefix(path, "iokit:")
	if locationStr == "" {
		return false
	}
	_, err := strconv.ParseUint(locationStr, 16, 32)
	return err == nil
}

// SysfsDevice is not used on macOS but included for compatibility.
type SysfsDevice struct{}

// ToUSBDevice is not implemented on macOS.
func (s *SysfsDevice) ToUSBDevice() *Device {
	return nil
}

// SysfsEnumerator is not used on macOS but included for compatibility.
type SysfsEnumerator struct{}

// NewSysfsEnumerator returns nil on macOS.
func NewSysfsEnumerator() *SysfsEnumerator {
	return nil
}
