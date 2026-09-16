//go:build darwin && !cgo

// Device and handle types for the CGO-free macOS backend, mirroring what
// iokit_darwin.go and device_darwin.go provide for the cgo build.
//
// Field names and method signatures deliberately match the cgo backend, so that
// transfer_darwin.go and compat_darwin.go — which are built in both
// configurations — need no changes.
//
// Enumeration is implemented. Opening a device needs IOKit's COM-style
// interfaces, which is the next stage, so anything requiring an open device
// reports ErrNotSupported rather than a nil error. See issue #14.

package usb

import (
	"strconv"
	"strings"
	"sync"
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
	Path        string
	Bus         uint8
	Address     uint8
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
// Obtaining one requires IOCreatePlugInInterfaceForService and QueryInterface,
// which is not implemented yet, so this is never non-nil in this build.
type IOUSBDeviceInterface struct {
	// ptr is the interface pointer IOKit returns, a pointer to a pointer to a
	// table of function pointers.
	ptr uintptr
}

// IOUSBInterfaceInterface wraps IOKit's IOUSBInterfaceInterface.
type IOUSBInterfaceInterface struct {
	ptr uintptr
}

// DeviceHandle represents an open USB device on macOS.
type DeviceHandle struct {
	device        *Device
	devInterface  *IOUSBDeviceInterface
	interfaces    map[uint8]*IOUSBInterfaceInterface
	claimedIfaces map[uint8]bool
	mu            sync.RWMutex
	closed        bool
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
// Not implemented in the CGO-free backend yet: it requires calling methods on
// IOKit's COM-style device interface. Build with cgo enabled for a backend that
// can open devices.
func (d *Device) Open() (*DeviceHandle, error) {
	return nil, ErrNotSupported
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
