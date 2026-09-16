//go:build darwin && !cgo

// Calls on an IOUSBDeviceInterface, dispatched through its method table.
//
// Only the getters are implemented here. They are the safe place to start:
// each takes a single out-pointer, none has a side effect, and every value they
// return is already known from the IOKit registry, so the method table can be
// checked against an external oracle rather than trusted. Opening the device and
// transferring data come next. See issue #14.

package usb

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// usbDeviceInterface wraps an IOUSBDeviceInterface handle.
//
// The zero value is unusable; obtain one with deviceInterfaceForService.
type usbDeviceInterface struct {
	handle unsafe.Pointer
}

// vtable returns the method table, or nil if the handle is unusable.
func (d *usbDeviceInterface) vtable() *ioUSBDeviceInterface {
	if d == nil || d.handle == nil {
		return nil
	}
	table := *(*unsafe.Pointer)(d.handle)
	if table == nil {
		return nil
	}
	return (*ioUSBDeviceInterface)(table)
}

// release drops the interface.
func (d *usbDeviceInterface) release() {
	v := d.vtable()
	if v == nil || v.Release == 0 {
		return
	}
	purego.SyscallN(v.Release, uintptr(d.handle))
	d.handle = nil
}

// callOut invokes a method whose only argument is an out-pointer, and reports
// the IOReturn it produced.
//
// out must point at storage large enough for what the method writes: the callee
// is C and will not check.
func (d *usbDeviceInterface) callOut(fn uintptr, out unsafe.Pointer) error {
	if fn == 0 {
		return ErrNotSupported
	}
	ret, _, _ := purego.SyscallN(fn, uintptr(d.handle), uintptr(out))
	if int32(ret) != kernSuccess {
		return ErrIO
	}
	return nil
}

func (d *usbDeviceInterface) getU8(fn uintptr) (uint8, error) {
	var value uint8
	if err := d.callOut(fn, unsafe.Pointer(&value)); err != nil {
		return 0, err
	}
	return value, nil
}

func (d *usbDeviceInterface) getU16(fn uintptr) (uint16, error) {
	var value uint16
	if err := d.callOut(fn, unsafe.Pointer(&value)); err != nil {
		return 0, err
	}
	return value, nil
}

func (d *usbDeviceInterface) getU32(fn uintptr) (uint32, error) {
	var value uint32
	if err := d.callOut(fn, unsafe.Pointer(&value)); err != nil {
		return 0, err
	}
	return value, nil
}

// The getters. Each mirrors one entry in the method table.

func (d *usbDeviceInterface) DeviceClass() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceClass)
}

func (d *usbDeviceInterface) DeviceSubClass() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceSubClass)
}

func (d *usbDeviceInterface) DeviceProtocol() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceProtocol)
}

func (d *usbDeviceInterface) VendorID() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceVendor)
}

func (d *usbDeviceInterface) ProductID() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceProduct)
}

func (d *usbDeviceInterface) ReleaseNumber() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceReleaseNumber)
}

// DeviceAddress returns the USB device address. USBDeviceAddress is a UInt16.
func (d *usbDeviceInterface) DeviceAddress() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceAddress)
}

func (d *usbDeviceInterface) DeviceSpeed() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceSpeed)
}

func (d *usbDeviceInterface) NumberOfConfigurations() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetNumberOfConfigurations)
}

func (d *usbDeviceInterface) LocationID() (uint32, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU32(v.GetLocationID)
}

// openDeviceInterface obtains a device interface for a registry entry, wrapped
// for method dispatch.
func openDeviceInterface(service uint32) (*usbDeviceInterface, error) {
	handle, err := deviceInterfaceForService(service)
	if err != nil {
		return nil, err
	}
	return &usbDeviceInterface{handle: handle}, nil
}
