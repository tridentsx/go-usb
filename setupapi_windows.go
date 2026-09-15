package usb

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SetupAPI GUIDs and constants
var (
	// GUID_DEVINTERFACE_USB_DEVICE is the device interface GUID for USB devices
	GUID_DEVINTERFACE_USB_DEVICE = windows.GUID{
		Data1: 0xA5DCBF10,
		Data2: 0x6530,
		Data3: 0x11D2,
		Data4: [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED},
	}

	// GUID_DEVINTERFACE_USB_HUB is the device interface GUID for USB hubs,
	// which do not register the generic USB device interface.
	GUID_DEVINTERFACE_USB_HUB = windows.GUID{
		Data1: 0xF18A0E88,
		Data2: 0xC30C,
		Data3: 0x11D0,
		Data4: [8]byte{0x88, 0x15, 0x00, 0xA0, 0xC9, 0x06, 0xBE, 0xD8},
	}

	// GUID for WinUSB devices
	GUID_DEVINTERFACE_WINUSB = windows.GUID{
		Data1: 0xDEE824EF,
		Data2: 0x729B,
		Data3: 0x4A0E,
		Data4: [8]byte{0x9C, 0x14, 0xB7, 0x11, 0x7D, 0x33, 0xA8, 0x17},
	}
)

const (
	DIGCF_PRESENT         = 0x00000002
	DIGCF_DEVICEINTERFACE = 0x00000010

	ERROR_NO_MORE_ITEMS = 259
)

var (
	modsetupapi = windows.NewLazySystemDLL("setupapi.dll")

	procSetupDiGetClassDevsW              = modsetupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces       = modsetupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW  = modsetupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceInfoList      = modsetupapi.NewProc("SetupDiDestroyDeviceInfoList")
	procSetupDiGetDeviceRegistryPropertyW = modsetupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	procSetupDiEnumDeviceInfo             = modsetupapi.NewProc("SetupDiEnumDeviceInfo")
)

// SP_DEVINFO_DATA structure
type spDevinfoData struct {
	cbSize    uint32
	ClassGUID windows.GUID
	DevInst   uint32
	Reserved  uintptr
}

// SP_DEVICE_INTERFACE_DATA structure
type spDeviceInterfaceData struct {
	cbSize             uint32
	InterfaceClassGUID windows.GUID
	Flags              uint32
	Reserved           uintptr
}

// SP_DEVICE_INTERFACE_DETAIL_DATA structure (variable size)
type spDeviceInterfaceDetailData struct {
	cbSize     uint32
	DevicePath [1]uint16 // Variable length
}

// SetupDiGetClassDevs returns a device information set
func setupDiGetClassDevs(classGUID *windows.GUID, enumerator *uint16, hwndParent uintptr, flags uint32) (windows.Handle, error) {
	r0, _, e1 := syscall.SyscallN(
		procSetupDiGetClassDevsW.Addr(),
		uintptr(unsafe.Pointer(classGUID)),
		uintptr(unsafe.Pointer(enumerator)),
		hwndParent,
		uintptr(flags),
	)
	handle := windows.Handle(r0)
	if handle == windows.InvalidHandle {
		return handle, e1
	}
	return handle, nil
}

// SetupDiEnumDeviceInterfaces enumerates device interfaces
func setupDiEnumDeviceInterfaces(devInfoSet windows.Handle, devInfoData *spDevinfoData, interfaceClassGUID *windows.GUID, memberIndex uint32, deviceInterfaceData *spDeviceInterfaceData) error {
	r0, _, e1 := syscall.SyscallN(
		procSetupDiEnumDeviceInterfaces.Addr(),
		uintptr(devInfoSet),
		uintptr(unsafe.Pointer(devInfoData)),
		uintptr(unsafe.Pointer(interfaceClassGUID)),
		uintptr(memberIndex),
		uintptr(unsafe.Pointer(deviceInterfaceData)),
	)
	if r0 == 0 {
		return e1
	}
	return nil
}

// SetupDiGetDeviceInterfaceDetail gets device interface detail
func setupDiGetDeviceInterfaceDetail(devInfoSet windows.Handle, deviceInterfaceData *spDeviceInterfaceData, deviceInterfaceDetailData *spDeviceInterfaceDetailData, deviceInterfaceDetailDataSize uint32, requiredSize *uint32, deviceInfoData *spDevinfoData) error {
	r0, _, e1 := syscall.SyscallN(
		procSetupDiGetDeviceInterfaceDetailW.Addr(),
		uintptr(devInfoSet),
		uintptr(unsafe.Pointer(deviceInterfaceData)),
		uintptr(unsafe.Pointer(deviceInterfaceDetailData)),
		uintptr(deviceInterfaceDetailDataSize),
		uintptr(unsafe.Pointer(requiredSize)),
		uintptr(unsafe.Pointer(deviceInfoData)),
	)
	if r0 == 0 {
		return e1
	}
	return nil
}

// SetupDiDestroyDeviceInfoList destroys a device information set
func setupDiDestroyDeviceInfoList(devInfoSet windows.Handle) error {
	r0, _, e1 := syscall.SyscallN(
		procSetupDiDestroyDeviceInfoList.Addr(),
		uintptr(devInfoSet),
	)
	if r0 == 0 {
		return e1
	}
	return nil
}

// WindowsUSBDevice represents a USB device found via SetupAPI
type WindowsUSBDevice struct {
	DevicePath   string
	InstanceID   string
	FriendlyName string
	HardwareID   string
	Bus          uint8
	Address      uint8

	// DevInst is the devnode handle for this interface, used to walk the
	// device tree and find the hub the device is attached to.
	DevInst uint32

	// WinUSB reports whether this path was found through the WinUSB interface
	// class, which is what determines whether it can be opened for I/O.
	WinUSB bool
}

// EnumerateUSBDevices enumerates all USB devices using SetupAPI.
//
// The WinUSB, generic USB device and USB hub interface classes are all queried
// and the results merged, deduplicated by device path. Hubs are included so
// that output is comparable with lsusb on Linux, which lists them.
//
// Querying only one class is not enough. The WinUSB class covers devices bound
// to WinUSB, including individual functions of composite devices, while the
// generic class is registered by the hub driver for every device regardless of
// which function driver owns it. A device can appear under either or both.
func EnumerateUSBDevices() ([]*WindowsUSBDevice, error) {
	var (
		devices  []*WindowsUSBDevice
		seen     = make(map[string]bool)
		firstErr error
	)

	for _, guid := range []*windows.GUID{
		&GUID_DEVINTERFACE_WINUSB,
		&GUID_DEVINTERFACE_USB_DEVICE,
		&GUID_DEVINTERFACE_USB_HUB,
	} {
		isWinUSB := guid == &GUID_DEVINTERFACE_WINUSB
		found, err := enumerateWithGUID(guid)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		for _, dev := range found {
			key := strings.ToLower(dev.DevicePath)
			if seen[key] {
				continue
			}

			// Root hubs expose no vendor or product ID in their device path,
			// so they would appear as 0000:0000 with no descriptor. Skip them
			// until they can be described from the host controller.
			if strings.Contains(key, "root_hub") {
				continue
			}

			seen[key] = true
			dev.WinUSB = isWinUSB
			devices = append(devices, dev)
		}
	}

	// Only report an error when nothing at all could be enumerated.
	if len(devices) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return devices, nil
}

func enumerateWithGUID(guid *windows.GUID) ([]*WindowsUSBDevice, error) {
	// Get device information set for USB devices
	devInfoSet, err := setupDiGetClassDevs(
		guid,
		nil,
		0,
		DIGCF_PRESENT|DIGCF_DEVICEINTERFACE,
	)
	if err != nil {
		return nil, fmt.Errorf("SetupDiGetClassDevs failed: %w", err)
	}
	defer setupDiDestroyDeviceInfoList(devInfoSet)

	var devices []*WindowsUSBDevice

	// Enumerate device interfaces
	for i := uint32(0); ; i++ {
		var interfaceData spDeviceInterfaceData
		interfaceData.cbSize = uint32(unsafe.Sizeof(interfaceData))

		err := setupDiEnumDeviceInterfaces(devInfoSet, nil, guid, i, &interfaceData)
		if err != nil {
			if errno, ok := err.(syscall.Errno); ok && errno == ERROR_NO_MORE_ITEMS {
				break
			}
			continue
		}

		// Get required size
		var requiredSize uint32
		setupDiGetDeviceInterfaceDetail(devInfoSet, &interfaceData, nil, 0, &requiredSize, nil)
		if requiredSize == 0 {
			continue
		}

		// Allocate buffer for detail data
		detailBuf := make([]byte, requiredSize)
		detailData := (*spDeviceInterfaceDetailData)(unsafe.Pointer(&detailBuf[0]))
		// cbSize must be set to the size of the fixed portion of the structure
		// On 64-bit Windows, this is 8 bytes (4 for cbSize + padding)
		// On 32-bit Windows, this is 6 bytes
		if unsafe.Sizeof(uintptr(0)) == 8 {
			detailData.cbSize = 8
		} else {
			detailData.cbSize = 6
		}

		var devInfoData spDevinfoData
		devInfoData.cbSize = uint32(unsafe.Sizeof(devInfoData))

		err = setupDiGetDeviceInterfaceDetail(devInfoSet, &interfaceData, detailData, requiredSize, nil, &devInfoData)
		if err != nil {
			continue
		}

		// Extract device path
		devicePath := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&detailData.DevicePath[0])))

		device := &WindowsUSBDevice{
			DevicePath: devicePath,
			DevInst:    devInfoData.DevInst,
		}

		devices = append(devices, device)
	}

	return devices, nil
}
