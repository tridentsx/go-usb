//go:build darwin

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

// vtable returns the method table, or nil if the handle is unusable.
func (d *IOUSBDeviceInterface) vtable() *ioUSBDeviceInterface {
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
func (d *IOUSBDeviceInterface) release() {
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
func (d *IOUSBDeviceInterface) callOut(fn uintptr, out unsafe.Pointer) error {
	if fn == 0 {
		return ErrNotSupported
	}
	ret, _, _ := purego.SyscallN(fn, uintptr(d.handle), uintptr(out))
	if int32(ret) != kernSuccess {
		return ErrIO
	}
	return nil
}

func (d *IOUSBDeviceInterface) getU8(fn uintptr) (uint8, error) {
	var value uint8
	if err := d.callOut(fn, unsafe.Pointer(&value)); err != nil {
		return 0, err
	}
	return value, nil
}

func (d *IOUSBDeviceInterface) getU16(fn uintptr) (uint16, error) {
	var value uint16
	if err := d.callOut(fn, unsafe.Pointer(&value)); err != nil {
		return 0, err
	}
	return value, nil
}

func (d *IOUSBDeviceInterface) getU32(fn uintptr) (uint32, error) {
	var value uint32
	if err := d.callOut(fn, unsafe.Pointer(&value)); err != nil {
		return 0, err
	}
	return value, nil
}

// The getters. Each mirrors one entry in the method table.

func (d *IOUSBDeviceInterface) DeviceClass() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceClass)
}

func (d *IOUSBDeviceInterface) DeviceSubClass() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceSubClass)
}

func (d *IOUSBDeviceInterface) DeviceProtocol() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceProtocol)
}

func (d *IOUSBDeviceInterface) VendorID() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceVendor)
}

func (d *IOUSBDeviceInterface) ProductID() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceProduct)
}

func (d *IOUSBDeviceInterface) ReleaseNumber() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceReleaseNumber)
}

// DeviceAddress returns the USB device address. USBDeviceAddress is a UInt16.
func (d *IOUSBDeviceInterface) DeviceAddress() (uint16, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU16(v.GetDeviceAddress)
}

func (d *IOUSBDeviceInterface) DeviceSpeed() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetDeviceSpeed)
}

func (d *IOUSBDeviceInterface) NumberOfConfigurations() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetNumberOfConfigurations)
}

func (d *IOUSBDeviceInterface) LocationID() (uint32, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU32(v.GetLocationID)
}

// openDeviceInterface obtains a device interface for a registry entry, wrapped
// for method dispatch.
func openDeviceInterface(service uint32) (*IOUSBDeviceInterface, error) {
	handle, err := deviceInterfaceForService(service)
	if err != nil {
		return nil, err
	}
	return &IOUSBDeviceInterface{handle: handle}, nil
}

// open claims the device for exclusive access via USBDeviceOpen.
func (d *IOUSBDeviceInterface) open() error {
	v := d.vtable()
	if v == nil || v.USBDeviceOpen == 0 {
		return ErrDeviceNotFound
	}
	ret, _, _ := purego.SyscallN(v.USBDeviceOpen, uintptr(d.handle))
	switch int32(ret) {
	case kernSuccess:
		return nil
	case kIOReturnExclusiveAccess:
		return ErrDeviceBusy
	case kIOReturnNotPermitted:
		return ErrPermissionDenied
	default:
		return ErrIO
	}
}

// closeDevice releases exclusive access. The interface itself stays valid.
func (d *IOUSBDeviceInterface) closeDevice() error {
	v := d.vtable()
	if v == nil || v.USBDeviceClose == 0 {
		return nil
	}
	purego.SyscallN(v.USBDeviceClose, uintptr(d.handle))
	return nil
}

// Configuration reads the active configuration value.
func (d *IOUSBDeviceInterface) Configuration() (uint8, error) {
	v := d.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}
	return d.getU8(v.GetConfiguration)
}

// SetConfiguration selects a configuration by value.
func (d *IOUSBDeviceInterface) SetConfiguration(config uint8) error {
	v := d.vtable()
	if v == nil || v.SetConfiguration == 0 {
		return ErrDeviceNotFound
	}
	ret, _, _ := purego.SyscallN(v.SetConfiguration, uintptr(d.handle), uintptr(config))
	if int32(ret) != kernSuccess {
		return ErrIO
	}
	return nil
}

// CreateInterfaceIterator returns an iterator over the device's interfaces
// matching req. Every field of req set to kIOUSBFindInterfaceDontCare returns
// every interface, at every alternate setting, as a separate io_service_t; the
// caller is responsible for releasing both the iterator and each service it
// yields.
func (d *IOUSBDeviceInterface) CreateInterfaceIterator(req *ioUSBFindInterfaceRequest) (uint32, error) {
	v := d.vtable()
	if v == nil || v.CreateInterfaceIterator == 0 {
		return 0, ErrDeviceNotFound
	}

	var iterator uint32
	ret, _, _ := purego.SyscallN(v.CreateInterfaceIterator,
		uintptr(d.handle), uintptr(unsafe.Pointer(req)), uintptr(unsafe.Pointer(&iterator)))
	if int32(ret) != kernSuccess {
		return 0, ErrIO
	}
	return iterator, nil
}

// ControlTransfer performs a control transfer on the default control endpoint.
//
// The base IOUSBDeviceInterface offers DeviceRequest, which has no timeout
// parameter; the timeout is therefore accepted and ignored. Honouring it needs
// DeviceRequestTO from a later interface version, which is not yet transcribed.
func (d *IOUSBDeviceInterface) ControlTransfer(bmRequestType, bRequest uint8, wValue, wIndex uint16, data []byte, timeout uint32) (int, error) {
	v := d.vtable()
	if v == nil || v.DeviceRequest == 0 {
		return 0, ErrDeviceNotFound
	}

	req := ioUSBDevRequest{
		bmRequestType: bmRequestType,
		bRequest:      bRequest,
		wValue:        wValue,
		wIndex:        wIndex,
		wLength:       uint16(len(data)),
	}
	if len(data) > 0 {
		req.pData = uintptr(unsafe.Pointer(&data[0]))
	}

	ret, _, _ := purego.SyscallN(v.DeviceRequest, uintptr(d.handle), uintptr(unsafe.Pointer(&req)))
	if int32(ret) != kernSuccess {
		return 0, ErrIO
	}
	return int(req.wLenDone), nil
}
