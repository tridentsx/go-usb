//go:build darwin && !cgo

// Calls on an IOUSBInterfaceInterface, dispatched through its method table,
// plus finding the io_service_t for a specific interface number.
//
// Claiming an interface needs its own COM interface, obtained the same way as
// the device interface in purego_device_interface_darwin.go: a plug-in for the
// interface's own io_service_t, queried for IOUSBInterfaceInterface300. The
// interface's io_service_t itself comes from the device interface's
// CreateInterfaceIterator, which enumerates the device's interfaces without
// needing to walk the IORegistry tree by hand.
//
// Bulk and interrupt transfers here are synchronous (ReadPipe/WritePipe and
// their timeout variants), mirroring what DeviceHandle.bulkTransfer in
// transfer_darwin.go actually calls. Asynchronous transfers
// (ReadPipeAsync/WritePipeAsync) are not implemented: unlike a synchronous
// call, where the buffer is provably still alive on the calling goroutine's
// stack for the syscall's duration, an async completion fires later on
// whatever goroutine services the run loop, so the buffer must stay reachable
// (and the transfer bookkeeping registered) until that happens. That's real,
// separate work, not a small addition to what is here. See issue #14.

package usb

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// vtable returns the method table, or nil if the handle is unusable.
func (i *IOUSBInterfaceInterface) vtable() *ioUSBInterfaceInterface300 {
	if i == nil || i.handle == nil {
		return nil
	}
	table := *(*unsafe.Pointer)(i.handle)
	if table == nil {
		return nil
	}
	return (*ioUSBInterfaceInterface300)(table)
}

// release drops the interface.
func (i *IOUSBInterfaceInterface) release() {
	if i == nil || i.handle == nil {
		return
	}
	releaseCOMInterface(i.handle)
	i.handle = nil
}

// open claims the interface for exclusive access via USBInterfaceOpen.
func (i *IOUSBInterfaceInterface) open() error {
	v := i.vtable()
	if v == nil || v.USBInterfaceOpen == 0 {
		return ErrDeviceNotFound
	}
	ret, _, _ := purego.SyscallN(v.USBInterfaceOpen, uintptr(i.handle))
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

// closeInterface releases exclusive access. The interface handle itself stays
// valid until release.
func (i *IOUSBInterfaceInterface) closeInterface() error {
	v := i.vtable()
	if v == nil || v.USBInterfaceClose == 0 {
		return nil
	}
	purego.SyscallN(v.USBInterfaceClose, uintptr(i.handle))
	return nil
}

// InterfaceNumber returns the bInterfaceNumber this interface was opened for.
func (i *IOUSBInterfaceInterface) InterfaceNumber() (uint8, error) {
	v := i.vtable()
	if v == nil || v.GetInterfaceNumber == 0 {
		return 0, ErrDeviceNotFound
	}
	var num uint8
	ret, _, _ := purego.SyscallN(v.GetInterfaceNumber, uintptr(i.handle), uintptr(unsafe.Pointer(&num)))
	if int32(ret) != kernSuccess {
		return 0, ErrIO
	}
	return num, nil
}

// SetAlternateSetting selects an alternate setting on the interface.
func (i *IOUSBInterfaceInterface) SetAlternateSetting(altSetting uint8) error {
	v := i.vtable()
	if v == nil || v.SetAlternateInterface == 0 {
		return ErrDeviceNotFound
	}
	ret, _, _ := purego.SyscallN(v.SetAlternateInterface, uintptr(i.handle), uintptr(altSetting))
	if int32(ret) != kernSuccess {
		return ErrIO
	}
	return nil
}

// ClearPipeStall clears a stall condition on a pipe.
func (i *IOUSBInterfaceInterface) ClearPipeStall(pipeRef uint8) error {
	v := i.vtable()
	if v == nil || v.ClearPipeStall == 0 {
		return ErrDeviceNotFound
	}
	ret, _, _ := purego.SyscallN(v.ClearPipeStall, uintptr(i.handle), uintptr(pipeRef))
	if int32(ret) != kernSuccess {
		return ErrIO
	}
	return nil
}

// BulkTransferOut writes to a bulk or interrupt pipe.
//
// A zero timeout uses WritePipe, which blocks with no timeout at all, exactly
// as the cgo backend's BulkTransfer C helper does; a nonzero timeout uses
// WritePipeTO with that value as both the no-data and completion timeout.
func (i *IOUSBInterfaceInterface) BulkTransferOut(pipeRef uint8, data []byte, timeout uint32) (int, error) {
	v := i.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}

	var buf unsafe.Pointer
	if len(data) > 0 {
		buf = unsafe.Pointer(&data[0])
	}

	var ret int64
	if timeout == 0 {
		if v.WritePipe == 0 {
			return 0, ErrNotSupported
		}
		r, _, _ := purego.SyscallN(v.WritePipe, uintptr(i.handle), uintptr(pipeRef), uintptr(buf), uintptr(len(data)))
		ret = int64(r)
	} else {
		if v.WritePipeTO == 0 {
			return 0, ErrNotSupported
		}
		r, _, _ := purego.SyscallN(v.WritePipeTO, uintptr(i.handle), uintptr(pipeRef), uintptr(buf),
			uintptr(len(data)), uintptr(timeout), uintptr(timeout))
		ret = int64(r)
	}

	if int32(ret) == kIOUSBTransactionTimeout {
		return 0, ErrTimeout
	}
	if int32(ret) != kernSuccess {
		return 0, ErrIO
	}
	return len(data), nil
}

// BulkTransferIn reads from a bulk or interrupt pipe.
//
// ReadPipe and ReadPipeTO take size as an in/out UInt32*: the caller supplies
// the buffer capacity and IOKit overwrites it with the number of bytes
// actually read.
func (i *IOUSBInterfaceInterface) BulkTransferIn(pipeRef uint8, data []byte, timeout uint32) (int, error) {
	v := i.vtable()
	if v == nil {
		return 0, ErrDeviceNotFound
	}

	var buf unsafe.Pointer
	if len(data) > 0 {
		buf = unsafe.Pointer(&data[0])
	}
	size := uint32(len(data))

	var ret int64
	if timeout == 0 {
		if v.ReadPipe == 0 {
			return 0, ErrNotSupported
		}
		r, _, _ := purego.SyscallN(v.ReadPipe, uintptr(i.handle), uintptr(pipeRef), uintptr(buf), uintptr(unsafe.Pointer(&size)))
		ret = int64(r)
	} else {
		if v.ReadPipeTO == 0 {
			return 0, ErrNotSupported
		}
		r, _, _ := purego.SyscallN(v.ReadPipeTO, uintptr(i.handle), uintptr(pipeRef), uintptr(buf),
			uintptr(unsafe.Pointer(&size)), uintptr(timeout), uintptr(timeout))
		ret = int64(r)
	}

	if int32(ret) == kIOUSBTransactionTimeout {
		return 0, ErrTimeout
	}
	if int32(ret) != kernSuccess {
		return 0, ErrIO
	}
	return int(size), nil
}

// openInterfaceInterface obtains an interface interface for a registry entry,
// wrapped for method dispatch. The caller owns the io_service_t and releases
// it independently; the returned interface holds its own reference once
// obtained, the same relationship openDeviceInterface has to the device's
// io_service_t.
func openInterfaceInterface(service uint32) (*IOUSBInterfaceInterface, error) {
	handle, err := interfaceInterfaceForService(service)
	if err != nil {
		return nil, err
	}
	return &IOUSBInterfaceInterface{handle: handle}, nil
}

// findInterfaceService returns the io_service_t for one alternate setting of
// one interface of dev, matched by reading bInterfaceNumber and
// bAlternateSetting registry properties off every service
// CreateInterfaceIterator returns, since IOUSBFindInterfaceRequest itself has
// no interface-number field to match on.
//
// The caller owns the returned service and must release it.
func findInterfaceService(dev *IOUSBDeviceInterface, iface, altSetting uint8) (uint32, error) {
	k, err := loadIOKit()
	if err != nil {
		return 0, err
	}

	req := ioUSBFindInterfaceRequest{
		bInterfaceClass:    kIOUSBFindInterfaceDontCare,
		bInterfaceSubClass: kIOUSBFindInterfaceDontCare,
		bInterfaceProtocol: kIOUSBFindInterfaceDontCare,
		bAlternateSetting:  kIOUSBFindInterfaceDontCare,
	}
	iterator, err := dev.CreateInterfaceIterator(&req)
	if err != nil {
		return 0, err
	}
	defer k.IOObjectRelease(iterator)

	for {
		service := k.IOIteratorNext(iterator)
		if service == 0 {
			break
		}

		var props uintptr
		if r := k.IORegistryEntryCreateCFProperties(service, &props, 0, 0); r == kernSuccess && props != 0 {
			num, haveNum := k.propertyNumber(props, propInterfaceNumber)
			alt, haveAlt := k.propertyNumber(props, propAlternateSetting)
			k.CFRelease(props)

			if haveNum && uint8(num) == iface && (!haveAlt || uint8(alt) == altSetting) {
				return service, nil
			}
		}
		k.IOObjectRelease(service)
	}

	return 0, ErrDeviceNotFound
}
