//go:build darwin

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
	"fmt"
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
	i.stopAsyncPump()
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

// NumEndpoints returns the number of endpoints on the interface's current
// alternate setting, not counting the implicit control pipe.
func (i *IOUSBInterfaceInterface) NumEndpoints() (uint8, error) {
	v := i.vtable()
	if v == nil || v.GetNumEndpoints == 0 {
		return 0, ErrDeviceNotFound
	}
	var num uint8
	ret, _, _ := purego.SyscallN(v.GetNumEndpoints, uintptr(i.handle), uintptr(unsafe.Pointer(&num)))
	if int32(ret) != kernSuccess {
		return 0, ErrIO
	}
	return num, nil
}

// pipeProperties describes one pipe as GetPipeProperties reports it.
type pipeProperties struct {
	direction     uint8 // 0 = OUT, 1 = IN (kUSBOut/kUSBIn from IOKit/usb/USBSpec.h)
	number        uint8 // the endpoint number, without the direction bit
	transferType  uint8
	maxPacketSize uint16
	interval      uint8
}

// GetPipeProperties describes the pipe at pipeRef.
func (i *IOUSBInterfaceInterface) GetPipeProperties(pipeRef uint8) (pipeProperties, error) {
	v := i.vtable()
	if v == nil || v.GetPipeProperties == 0 {
		return pipeProperties{}, ErrDeviceNotFound
	}
	var p pipeProperties
	ret, _, _ := purego.SyscallN(v.GetPipeProperties, uintptr(i.handle), uintptr(pipeRef),
		uintptr(unsafe.Pointer(&p.direction)), uintptr(unsafe.Pointer(&p.number)), uintptr(unsafe.Pointer(&p.transferType)),
		uintptr(unsafe.Pointer(&p.maxPacketSize)), uintptr(unsafe.Pointer(&p.interval)))
	if int32(ret) != kernSuccess {
		return pipeProperties{}, fmt.Errorf("GetPipeProperties(%d): IOReturn %#x: %w", pipeRef, uint32(ret), ErrIO)
	}
	return p, nil
}

// PipeRefForEndpoint finds the pipeRef for a USB endpoint address on the
// interface's current alternate setting.
//
// pipeRef is not the endpoint address, or the endpoint number, or anything
// derivable from the descriptor without asking IOKit: it is a 1-indexed
// position in the interface's own pipe table, in descriptor order, which
// GetPipeProperties is the only way to read. Deriving it as endpoint&0x0F,
// which both transfer_darwin.go's bulkTransfer and this package's earlier
// isochronous code did, happens to be right exactly when an interface's
// endpoint numbers are assigned in the same order they're indexed here, and
// wrong otherwise -- silently: IOKit reports a real, plausible-looking
// IOReturn for the wrong pipe rather than failing obviously.
func (i *IOUSBInterfaceInterface) PipeRefForEndpoint(endpoint uint8) (uint8, error) {
	n, err := i.NumEndpoints()
	if err != nil {
		return 0, err
	}
	wantDirection := uint8(0)
	if endpoint&0x80 != 0 {
		wantDirection = 1
	}
	wantNumber := endpoint & 0x0F

	for pipeRef := uint8(1); pipeRef <= n; pipeRef++ {
		p, err := i.GetPipeProperties(pipeRef)
		if err != nil {
			continue
		}
		if p.direction == wantDirection && p.number == wantNumber {
			return pipeRef, nil
		}
	}
	return 0, fmt.Errorf("no pipe for endpoint %#x on this alternate setting", endpoint)
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

// AbortPipe aborts all outstanding I/O on a pipe, including any isochronous
// transfer this process has in flight there. IOKit has no way to cancel one
// specific request; this is the mechanism it offers, and the aborted
// transfer(s) still complete normally through their callback, now with an
// error status.
func (i *IOUSBInterfaceInterface) AbortPipe(pipeRef uint8) error {
	v := i.vtable()
	if v == nil || v.AbortPipe == 0 {
		return ErrDeviceNotFound
	}
	ret, _, _ := purego.SyscallN(v.AbortPipe, uintptr(i.handle), uintptr(pipeRef))
	if int32(ret) != kernSuccess {
		return ErrIO
	}
	return nil
}

// GetBusFrameNumber returns the current USB bus frame number, the basis for
// choosing an isochronous transfer's start frame.
func (i *IOUSBInterfaceInterface) GetBusFrameNumber() (uint64, error) {
	v := i.vtable()
	if v == nil || v.GetBusFrameNumber == 0 {
		return 0, ErrDeviceNotFound
	}
	var frame uint64
	var atTime [2]uint32 // AbsoluteTime; its value is not used here
	ret, _, _ := purego.SyscallN(v.GetBusFrameNumber, uintptr(i.handle),
		uintptr(unsafe.Pointer(&frame)), uintptr(unsafe.Pointer(&atTime)))
	if int32(ret) != kernSuccess {
		return 0, fmt.Errorf("GetBusFrameNumber: IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return frame, nil
}

// CreateInterfaceAsyncEventSource creates the CFRunLoopSource IOKit delivers
// this interface's asynchronous completions through. The caller adds it to
// whichever run loop will service them.
func (i *IOUSBInterfaceInterface) CreateInterfaceAsyncEventSource() (uintptr, error) {
	v := i.vtable()
	if v == nil || v.CreateInterfaceAsyncEventSource == 0 {
		return 0, ErrDeviceNotFound
	}
	var source uintptr
	ret, _, _ := purego.SyscallN(v.CreateInterfaceAsyncEventSource, uintptr(i.handle), uintptr(unsafe.Pointer(&source)))
	if int32(ret) != kernSuccess || source == 0 {
		return 0, fmt.Errorf("CreateInterfaceAsyncEventSource: IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return source, nil
}

// readIsochPipeAsync and writeIsochPipeAsync submit an isochronous transfer.
// callback is IOKit's IOAsyncCallback1: void(*)(void *refcon, IOReturn
// result, void *arg0). IOKit sets arg0 to frameList on completion, which is
// how the shared callback in purego_isochronous_darwin.go finds its way back
// to the right *IsochronousTransfer without allocating a trampoline per
// transfer.
func (i *IOUSBInterfaceInterface) readIsochPipeAsync(pipeRef uint8, buf []byte, frameStart uint64, numFrames uint32, frameList *ioUSBIsocFrame, callback uintptr) error {
	v := i.vtable()
	if v == nil || v.ReadIsochPipeAsync == 0 {
		return ErrDeviceNotFound
	}
	var bufPtr unsafe.Pointer
	if len(buf) > 0 {
		bufPtr = unsafe.Pointer(&buf[0])
	}
	ret, _, _ := purego.SyscallN(v.ReadIsochPipeAsync, uintptr(i.handle), uintptr(pipeRef), uintptr(bufPtr),
		uintptr(frameStart), uintptr(numFrames), uintptr(unsafe.Pointer(frameList)), callback, 0)
	if int32(ret) != kernSuccess {
		return fmt.Errorf("ReadIsochPipeAsync: IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return nil
}

func (i *IOUSBInterfaceInterface) writeIsochPipeAsync(pipeRef uint8, buf []byte, frameStart uint64, numFrames uint32, frameList *ioUSBIsocFrame, callback uintptr) error {
	v := i.vtable()
	if v == nil || v.WriteIsochPipeAsync == 0 {
		return ErrDeviceNotFound
	}
	var bufPtr unsafe.Pointer
	if len(buf) > 0 {
		bufPtr = unsafe.Pointer(&buf[0])
	}
	ret, _, _ := purego.SyscallN(v.WriteIsochPipeAsync, uintptr(i.handle), uintptr(pipeRef), uintptr(bufPtr),
		uintptr(frameStart), uintptr(numFrames), uintptr(unsafe.Pointer(frameList)), callback, 0)
	if int32(ret) != kernSuccess {
		return fmt.Errorf("WriteIsochPipeAsync: IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return nil
}

// readPipeAsync and writePipeAsync submit an asynchronous bulk or interrupt
// transfer. Unlike the isochronous pair above, IOKit documents arg0 of the
// completion as the number of bytes transferred, not a pointer, so refcon
// (an address the caller controls) is what correlates a completion back to
// its transfer in purego_asynctransfer_darwin.go's pendingBulk map.
func (i *IOUSBInterfaceInterface) readPipeAsync(pipeRef uint8, buf []byte, callback, refcon uintptr) error {
	v := i.vtable()
	if v == nil || v.ReadPipeAsync == 0 {
		return ErrDeviceNotFound
	}
	var bufPtr unsafe.Pointer
	if len(buf) > 0 {
		bufPtr = unsafe.Pointer(&buf[0])
	}
	ret, _, _ := purego.SyscallN(v.ReadPipeAsync, uintptr(i.handle), uintptr(pipeRef), uintptr(bufPtr),
		uintptr(len(buf)), callback, refcon)
	if int32(ret) != kernSuccess {
		return fmt.Errorf("ReadPipeAsync: IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return nil
}

func (i *IOUSBInterfaceInterface) writePipeAsync(pipeRef uint8, buf []byte, callback, refcon uintptr) error {
	v := i.vtable()
	if v == nil || v.WritePipeAsync == 0 {
		return ErrDeviceNotFound
	}
	var bufPtr unsafe.Pointer
	if len(buf) > 0 {
		bufPtr = unsafe.Pointer(&buf[0])
	}
	ret, _, _ := purego.SyscallN(v.WritePipeAsync, uintptr(i.handle), uintptr(pipeRef), uintptr(bufPtr),
		uintptr(len(buf)), callback, refcon)
	if int32(ret) != kernSuccess {
		return fmt.Errorf("WritePipeAsync: IOReturn %#x: %w", uint32(ret), ErrIO)
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
		return 0, fmt.Errorf("WritePipe(TO): IOReturn %#x: %w", uint32(ret), ErrIO)
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
		return 0, fmt.Errorf("ReadPipe(TO): IOReturn %#x: %w", uint32(ret), ErrIO)
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
