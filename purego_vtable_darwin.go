//go:build darwin && !cgo

// IOKit COM-style vtable dispatch without cgo.
//
// IOKit exposes USB devices through plug-in interfaces modelled on COM: a
// handle is a pointer to a pointer to a structure of function pointers, and a
// method call reads the pointer out of that structure and calls it with the
// handle as its first argument. The cgo backend does this with small C shim
// functions; here purego.SyscallN calls the function pointer directly.
//
// This file obtains an IOUSBDeviceInterface. Calling methods on it is the next
// step and needs its own method table; that table is deliberately not guessed
// here. See issue #14.

package usb

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// Additional IOKit and CoreFoundation entry points needed for plug-ins.
type iokitPluginFuncs struct {
	CFUUIDCreateFromUUIDBytes         func(alloc uintptr, bytes cfUUIDBytes) uintptr
	IOCreatePlugInInterfaceForService func(service uint32, pluginType, interfaceType uintptr, theInterface *unsafe.Pointer, theScore *int32) int32

	// CFUUIDCreateString is used only to verify, at test time, that a UUID
	// built from raw bytes is the UUID intended. Passing the bytes in the wrong
	// order yields a valid object for the wrong UUID, which is otherwise
	// invisible until a plug-in lookup fails.
	CFUUIDCreateString func(alloc uintptr, uuid uintptr) uintptr
}

var iokitPlugin iokitPluginFuncs

// registerPluginFuncs resolves the plug-in entry points. Called from
// loadIOKit's once, so it shares its error handling.
func registerPluginFuncs(ioKit, cf uintptr) {
	purego.RegisterLibFunc(&iokitPlugin.CFUUIDCreateFromUUIDBytes, cf, "CFUUIDCreateFromUUIDBytes")
	purego.RegisterLibFunc(&iokitPlugin.IOCreatePlugInInterfaceForService, ioKit, "IOCreatePlugInInterfaceForService")
	purego.RegisterLibFunc(&iokitPlugin.CFUUIDCreateString, cf, "CFUUIDCreateString")
}

// hresultSuccess is S_OK, returned by QueryInterface on success.
const hresultSuccess = 0

// vtableOf returns the function-pointer table a COM-style interface handle
// points at.
//
// handle is an `Interface **`, so one dereference yields the pointer to the
// table. Callers must keep handle alive for as long as they use the result.
//
// Handles are carried as unsafe.Pointer rather than uintptr throughout, so that
// no uintptr is ever converted back into a pointer. That conversion is what
// go vet's unsafeptr check objects to, and rightly: a uintptr holds no reference,
// so it says nothing about the lifetime of what it points at. These particular
// addresses are IOKit's, not the Go heap's, but keeping the type honest is
// cheaper than arguing with the checker.
func vtableOf(handle unsafe.Pointer) *ioCFPlugInInterface {
	if handle == nil {
		return nil
	}
	table := *(*unsafe.Pointer)(handle)
	if table == nil {
		return nil
	}
	return (*ioCFPlugInInterface)(table)
}

// deviceInterfaceForService obtains an IOUSBDeviceInterface for a registry
// entry.
//
// This does not open the device. Opening is USBDeviceOpen, a method on the
// returned interface, so an interface can be obtained for devices another
// process holds.
//
// The caller owns the returned interface and must release it with
// releaseDeviceInterface.
func deviceInterfaceForService(service uint32) (unsafe.Pointer, error) {
	k, err := loadIOKit()
	if err != nil {
		return nil, err
	}

	pluginType := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(kIOUSBDeviceUserClientTypeID))
	if pluginType == 0 {
		return nil, ErrOther
	}
	defer k.CFRelease(pluginType)

	interfaceType := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(kIOCFPlugInInterfaceID))
	if interfaceType == 0 {
		return nil, ErrOther
	}
	defer k.CFRelease(interfaceType)

	var plugin unsafe.Pointer
	var score int32
	if ret := iokitPlugin.IOCreatePlugInInterfaceForService(
		service, pluginType, interfaceType, &plugin, &score); ret != kernSuccess || plugin == nil {
		return nil, ErrNotSupported
	}

	vtable := vtableOf(plugin)
	if vtable == nil || vtable.QueryInterface == 0 {
		return nil, ErrOther
	}

	// QueryInterface takes its REFIID by value. A 16-byte all-integer struct is
	// passed in two consecutive registers on both arm64 and amd64, in the same
	// order, so it becomes two arguments either way.
	lo, hi := uuidBytes(kIOUSBDeviceInterfaceID).words()

	var deviceInterface unsafe.Pointer
	result, _, _ := purego.SyscallN(vtable.QueryInterface,
		uintptr(plugin), lo, hi, uintptr(unsafe.Pointer(&deviceInterface)))

	// The plug-in is no longer needed once the device interface exists: the
	// device interface holds its own reference.
	if vtable.Release != 0 {
		purego.SyscallN(vtable.Release, uintptr(plugin))
	}

	if result != hresultSuccess || deviceInterface == nil {
		return nil, ErrNotSupported
	}
	return deviceInterface, nil
}

// releaseDeviceInterface drops a device interface obtained from
// deviceInterfaceForService.
//
// Release sits at the same offset in every COM-style IOKit interface, since they
// all begin with IUNKNOWN_C_GUTS, so the plug-in table describes it correctly.
func releaseDeviceInterface(deviceInterface unsafe.Pointer) {
	vtable := vtableOf(deviceInterface)
	if vtable == nil || vtable.Release == 0 {
		return
	}
	purego.SyscallN(vtable.Release, uintptr(deviceInterface))
}
