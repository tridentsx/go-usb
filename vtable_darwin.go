// IOKit COM-style vtable dispatch, without cgo.
//
// IOKit exposes USB devices through plug-in interfaces modelled on COM: a
// handle is a pointer to a pointer to a structure of function pointers, and a
// method call reads the pointer out of that structure and calls it with the
// handle as its first argument. purego.SyscallN calls the function pointer
// directly, in place of the small C shim functions a cgo binding would need.
//
// This file obtains an IOUSBDeviceInterface or IOUSBInterfaceInterface handle
// via QueryInterface; dispatching methods on the handle is
// device_interface_darwin.go and interface_darwin.go's job, through the
// method tables in iokit_types_darwin.go.

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

// IOKit return codes, from IOKit/IOReturn.h. sub_iokit_common errors are
// 0xe0000000 | (0x2bc + n).
const (
	kIOReturnExclusiveAccess = -0x1FFFFD3B // 0xe00002c5
	kIOReturnNotPermitted    = -0x1FFFFD3F // 0xe00002c1
	kIOReturnUnsupported     = -0x1FFFFD39 // 0xe00002c7

	// kIOUSBTransactionTimeout is IOUSBFamily's kIOUSBTransactionTimeout:
	// int32(-536870899) (0xe000000d). ReadPipeTO and WritePipeTO return this
	// when the transfer times out.
	kIOUSBTransactionTimeout = -536870899
)

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

// pluginInterfaceForService obtains a COM-style interface for a registry
// entry: IOCreatePlugInInterfaceForService for pluginType, then QueryInterface
// for target.
//
// This does not open anything. Opening is a method (USBDeviceOpen,
// USBInterfaceOpen) on the returned interface, so an interface can be obtained
// for a service another process holds.
//
// The caller owns the returned interface and must release it with
// releaseCOMInterface.
func pluginInterfaceForService(service uint32, pluginType, target [16]byte) (unsafe.Pointer, error) {
	k, err := loadIOKit()
	if err != nil {
		return nil, err
	}

	pluginTypeUUID := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(pluginType))
	if pluginTypeUUID == 0 {
		return nil, ErrOther
	}
	defer k.CFRelease(pluginTypeUUID)

	interfaceType := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(kIOCFPlugInInterfaceID))
	if interfaceType == 0 {
		return nil, ErrOther
	}
	defer k.CFRelease(interfaceType)

	var plugin unsafe.Pointer
	var score int32
	if ret := iokitPlugin.IOCreatePlugInInterfaceForService(
		service, pluginTypeUUID, interfaceType, &plugin, &score); ret != kernSuccess || plugin == nil {
		return nil, ErrNotSupported
	}

	vtable := vtableOf(plugin)
	if vtable == nil || vtable.QueryInterface == 0 {
		return nil, ErrOther
	}

	// QueryInterface takes its REFIID by value. A 16-byte all-integer struct is
	// passed in two consecutive registers on both arm64 and amd64, in the same
	// order, so it becomes two arguments either way.
	lo, hi := uuidBytes(target).words()

	var iface unsafe.Pointer
	result, _, _ := purego.SyscallN(vtable.QueryInterface,
		uintptr(plugin), lo, hi, uintptr(unsafe.Pointer(&iface)))

	// The plug-in is no longer needed once the target interface exists: that
	// interface holds its own reference.
	if vtable.Release != 0 {
		purego.SyscallN(vtable.Release, uintptr(plugin))
	}

	if result != hresultSuccess || iface == nil {
		return nil, ErrNotSupported
	}
	return iface, nil
}

// deviceInterfaceForService obtains an IOUSBDeviceInterface for a registry
// entry. See pluginInterfaceForService.
func deviceInterfaceForService(service uint32) (unsafe.Pointer, error) {
	return pluginInterfaceForService(service, kIOUSBDeviceUserClientTypeID, kIOUSBDeviceInterfaceID)
}

// interfaceInterfaceForService obtains an IOUSBInterfaceInterface for a
// registry entry describing one interface of a device. See
// pluginInterfaceForService.
func interfaceInterfaceForService(service uint32) (unsafe.Pointer, error) {
	return pluginInterfaceForService(service, kIOUSBInterfaceUserClientTypeID, kIOUSBInterfaceInterfaceID300)
}

// releaseCOMInterface drops an interface obtained from
// deviceInterfaceForService or interfaceInterfaceForService.
//
// Release sits at the same offset in every COM-style IOKit interface, since they
// all begin with IUNKNOWN_C_GUTS, so the plug-in table describes it correctly.
func releaseCOMInterface(iface unsafe.Pointer) {
	vtable := vtableOf(iface)
	if vtable == nil || vtable.Release == 0 {
		return
	}
	purego.SyscallN(vtable.Release, uintptr(iface))
}
