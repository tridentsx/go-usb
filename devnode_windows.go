package usb

import (
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Device tree navigation via cfgmgr32. golang.org/x/sys/windows wraps
// CM_Get_Device_Interface_List but not the parent or property calls, so those
// are bound here.

var (
	modcfgmgr32 = windows.NewLazySystemDLL("cfgmgr32.dll")

	procCM_Get_Parent                     = modcfgmgr32.NewProc("CM_Get_Parent")
	procCM_Get_Device_IDW                 = modcfgmgr32.NewProc("CM_Get_Device_IDW")
	procCM_Get_DevNode_Registry_PropertyW = modcfgmgr32.NewProc("CM_Get_DevNode_Registry_PropertyW")
)

// crSuccess is CR_SUCCESS from cfgmgr32.
const crSuccess = 0

// cmDRPAddress is CM_DRP_ADDRESS, the CM_ form of SPDRP_ADDRESS. For a USB
// device this property holds its port number on the parent hub.
const cmDRPAddress = 0x1D

// maxDeviceIDLength is MAX_DEVICE_ID_LEN.
const maxDeviceIDLength = 200

// cmGetParent returns the parent devnode of a devnode.
func cmGetParent(devInst uint32) (uint32, error) {
	var parent uint32
	r0, _, _ := syscall.SyscallN(procCM_Get_Parent.Addr(),
		uintptr(unsafe.Pointer(&parent)), uintptr(devInst), 0)
	if r0 != crSuccess {
		return 0, ErrNotFound
	}
	return parent, nil
}

// cmGetDeviceID returns a devnode's device instance ID, which is the string
// Device Manager shows as "Device instance path".
func cmGetDeviceID(devInst uint32) (string, error) {
	buf := make([]uint16, maxDeviceIDLength+1)
	r0, _, _ := syscall.SyscallN(procCM_Get_Device_IDW.Addr(),
		uintptr(devInst), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	if r0 != crSuccess {
		return "", ErrNotFound
	}
	return windows.UTF16ToString(buf), nil
}

// cmGetPortNumber reads CM_DRP_ADDRESS, a USB device's port on its parent hub.
func cmGetPortNumber(devInst uint32) (int, error) {
	var value uint32
	length := uint32(unsafe.Sizeof(value))
	r0, _, _ := syscall.SyscallN(procCM_Get_DevNode_Registry_PropertyW.Addr(),
		uintptr(devInst), uintptr(cmDRPAddress), 0,
		uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&length)), 0)
	if r0 != crSuccess {
		return 0, ErrNotFound
	}
	return int(value), nil
}

// hubInterfacePath returns the hub interface path for a devnode, or an error if
// the devnode is not a USB hub.
func hubInterfacePath(devInst uint32) (string, error) {
	id, err := cmGetDeviceID(devInst)
	if err != nil {
		return "", err
	}
	paths, err := windows.CM_Get_Device_Interface_List(id, &GUID_DEVINTERFACE_USB_HUB, 0)
	if err != nil || len(paths) == 0 {
		return "", ErrNotFound
	}
	return paths[0], nil
}

// deviceLocation describes where a device sits on the USB tree.
type deviceLocation struct {
	HubPath  string // parent hub's interface path
	HubInst  uint32 // parent hub's devnode
	Port     int    // port number on that hub
	RootInst uint32 // root hub devnode, used to derive a bus number
}

// locateDevice walks up the device tree from a devnode until it finds the hub
// the device hangs off, and reports the port it occupies.
//
// The walk is necessary because a composite device's function devnodes sit
// below the device devnode, which in turn sits below the hub. The port number
// belongs to the last devnode before the hub.
func locateDevice(devInst uint32) (deviceLocation, error) {
	const maxDepth = 16

	cur := devInst
	for depth := 0; depth < maxDepth; depth++ {
		parent, err := cmGetParent(cur)
		if err != nil {
			return deviceLocation{}, err
		}

		if hubPath, err := hubInterfacePath(parent); err == nil {
			port, err := cmGetPortNumber(cur)
			if err != nil {
				return deviceLocation{}, err
			}
			return deviceLocation{
				HubPath:  hubPath,
				HubInst:  parent,
				Port:     port,
				RootInst: rootHubOf(parent),
			}, nil
		}

		cur = parent
	}
	return deviceLocation{}, ErrNotFound
}

// rootHubOf walks up from a hub to the topmost USB devnode, which is the root
// hub. Devices sharing a root hub share a bus.
func rootHubOf(hubInst uint32) uint32 {
	const maxDepth = 16

	cur := hubInst
	for depth := 0; depth < maxDepth; depth++ {
		parent, err := cmGetParent(cur)
		if err != nil {
			return cur
		}
		id, err := cmGetDeviceID(parent)
		if err != nil {
			return cur
		}
		// The USB tree ends where the host controller begins, which is a PCI
		// or USB4 devnode rather than a USB one.
		if !strings.HasPrefix(strings.ToUpper(id), "USB\\") {
			return cur
		}
		cur = parent
	}
	return cur
}

// busNumbering assigns stable, one-based bus numbers to root hubs.
//
// Windows has no notion of a USB bus number, so this is synthetic. Root hubs
// are sorted by device instance ID so the numbering is stable across runs on
// the same machine, but it does not correspond to anything the OS reports.
func busNumbering(rootInsts map[uint32]bool) map[uint32]uint8 {
	ids := make([]string, 0, len(rootInsts))
	byID := make(map[string]uint32, len(rootInsts))
	for inst := range rootInsts {
		id, err := cmGetDeviceID(inst)
		if err != nil {
			continue
		}
		ids = append(ids, id)
		byID[id] = inst
	}
	sort.Strings(ids)

	buses := make(map[uint32]uint8, len(ids))
	for i, id := range ids {
		buses[byID[id]] = uint8(i + 1)
	}
	return buses
}

// devnodeHasAncestor reports whether target appears among a devnode's ancestors,
// or is the devnode itself.
//
// This is how a HID collection is attributed to a USB device: the collection's
// devnode sits below the HID device, which sits below the USB function or
// device, so walking up finds the device without relying on path text.
func devnodeHasAncestor(devInst, target uint32) bool {
	const maxDepth = 16

	cur := devInst
	for depth := 0; depth < maxDepth; depth++ {
		if cur == target {
			return true
		}
		parent, err := cmGetParent(cur)
		if err != nil {
			return false
		}
		cur = parent
	}
	return false
}
