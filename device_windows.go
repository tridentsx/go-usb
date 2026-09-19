package usb

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinUSB API bindings
var (
	modwinusb = windows.NewLazySystemDLL("winusb.dll")

	procWinUsb_Initialize                 = modwinusb.NewProc("WinUsb_Initialize")
	procWinUsb_Free                       = modwinusb.NewProc("WinUsb_Free")
	procWinUsb_GetAssociatedInterface     = modwinusb.NewProc("WinUsb_GetAssociatedInterface")
	procWinUsb_GetDescriptor              = modwinusb.NewProc("WinUsb_GetDescriptor")
	procWinUsb_QueryInterfaceSettings     = modwinusb.NewProc("WinUsb_QueryInterfaceSettings")
	procWinUsb_QueryDeviceInformation     = modwinusb.NewProc("WinUsb_QueryDeviceInformation")
	procWinUsb_SetCurrentAlternateSetting = modwinusb.NewProc("WinUsb_SetCurrentAlternateSetting")
	procWinUsb_GetCurrentAlternateSetting = modwinusb.NewProc("WinUsb_GetCurrentAlternateSetting")
	procWinUsb_QueryPipe                  = modwinusb.NewProc("WinUsb_QueryPipe")
	procWinUsb_SetPipePolicy              = modwinusb.NewProc("WinUsb_SetPipePolicy")
	procWinUsb_GetPipePolicy              = modwinusb.NewProc("WinUsb_GetPipePolicy")
	procWinUsb_ReadPipe                   = modwinusb.NewProc("WinUsb_ReadPipe")
	procWinUsb_WritePipe                  = modwinusb.NewProc("WinUsb_WritePipe")
	procWinUsb_ControlTransfer            = modwinusb.NewProc("WinUsb_ControlTransfer")
	procWinUsb_ResetPipe                  = modwinusb.NewProc("WinUsb_ResetPipe")
	procWinUsb_AbortPipe                  = modwinusb.NewProc("WinUsb_AbortPipe")
	procWinUsb_FlushPipe                  = modwinusb.NewProc("WinUsb_FlushPipe")
	procWinUsb_GetCurrentFrameNumber      = modwinusb.NewProc("WinUsb_GetCurrentFrameNumber")
	procWinUsb_GetAdjustedFrameNumber     = modwinusb.NewProc("WinUsb_GetAdjustedFrameNumber")
	procWinUsb_RegisterIsochBuffer        = modwinusb.NewProc("WinUsb_RegisterIsochBuffer")
	procWinUsb_UnregisterIsochBuffer      = modwinusb.NewProc("WinUsb_UnregisterIsochBuffer")
	procWinUsb_WriteIsochPipe             = modwinusb.NewProc("WinUsb_WriteIsochPipe")
	procWinUsb_ReadIsochPipe              = modwinusb.NewProc("WinUsb_ReadIsochPipe")
	procWinUsb_WriteIsochPipeAsap         = modwinusb.NewProc("WinUsb_WriteIsochPipeAsap")
	procWinUsb_ReadIsochPipeAsap          = modwinusb.NewProc("WinUsb_ReadIsochPipeAsap")
)

// WinUSB constants
const (
	// Device information types for WinUsb_QueryDeviceInformation
	DEVICE_SPEED = 1

	// Pipe policy types
	SHORT_PACKET_TERMINATE = 0x01
	AUTO_CLEAR_STALL       = 0x02
	PIPE_TRANSFER_TIMEOUT  = 0x03
	IGNORE_SHORT_PACKETS   = 0x04
	ALLOW_PARTIAL_READS    = 0x05
	AUTO_FLUSH             = 0x06
	RAW_IO                 = 0x07
	MAXIMUM_TRANSFER_SIZE  = 0x08
	RESET_PIPE_ON_RESUME   = 0x09

	// Device speeds
	LowSpeed   = 0x01
	FullSpeed  = 0x02
	HighSpeed  = 0x03
	SuperSpeed = 0x04
)

// USB_INTERFACE_DESCRIPTOR structure
type winusbInterfaceDescriptor struct {
	bLength            uint8
	bDescriptorType    uint8
	bInterfaceNumber   uint8
	bAlternateSetting  uint8
	bNumEndpoints      uint8
	bInterfaceClass    uint8
	bInterfaceSubClass uint8
	bInterfaceProtocol uint8
	iInterface         uint8
}

// WINUSB_PIPE_INFORMATION structure
type winusbPipeInformation struct {
	PipeType          uint32
	PipeId            uint8
	MaximumPacketSize uint16
	Interval          uint8
}

// WinUSB handle type
type winusbInterfaceHandle uintptr

// SysfsStrings is declared in types_common.go as an alias for DeviceStrings so
// that every platform exposes the same type.

// Device represents a USB device on Windows
type Device struct {
	Path          string
	Bus           uint8
	Address       uint8
	Port          uint8  // physical port number on the parent hub
	Speed         Speed  // negotiated speed, set from hub IOCTL during enumeration
	ParentHubAddr uint8  // USB address of the parent hub (0 = unknown / root hub)
	Descriptor    DeviceDescriptor
	Configs       []RawConfigDescriptor
	SysfsStrings  *SysfsStrings
	devicePath    string // Windows device path (e.g., \\?\usb#vid_xxxx&pid_xxxx...)

	// devInst is the devnode for this device, used to find the HID collections
	// that belong to it.
	devInst uint32

	// parentHubInst is the devnode of the hub this device is attached to.
	// Used in a second pass to populate ParentHubAddr.
	parentHubInst uint32

	// rawConfigs holds the configuration descriptors as read from the hub, kept
	// so that endpoints can be mapped onto HID collections without opening the
	// device.
	rawConfigs [][]byte
}

// utf16ToRunes converts UTF-16 to runes
func utf16ToRunes(u16 []uint16) []rune {
	runes := make([]rune, 0, len(u16))
	for _, v := range u16 {
		if v == 0 {
			break
		}
		runes = append(runes, rune(v))
	}
	return runes
}

// DeviceHandle represents an open USB device handle on Windows
type DeviceHandle struct {
	device           *Device
	fileHandle       windows.Handle
	winusbHandle     winusbInterfaceHandle
	interfaceHandles map[uint8]winusbInterfaceHandle
	claimedIfaces    map[uint8]bool
	mu               sync.RWMutex
	closed           bool
	currentConfig    int

	// hid is non-nil when the device is owned by the HID class driver and is
	// reached through hid.dll rather than WinUSB. It is the second transport
	// behind this handle.
	hid *hidDevice
}

// Open opens the USB device.
//
// WinUSB is tried first. If the device is owned by the HID class driver, which
// is what WinUsb_Initialize failing indicates for a HID device, the handle
// falls back to the HID transport: report-level access through hid.dll. See
// hid_windows.go for what that can and cannot do.
func (d *Device) Open() (*DeviceHandle, error) {
	// Open the device file
	pathPtr, err := windows.UTF16PtrFromString(d.devicePath)
	if err != nil {
		return nil, fmt.Errorf("invalid device path: %w", err)
	}

	fileHandle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		// The device file itself cannot be opened, which is normal for a device
		// owned by a class driver. HID devices are still reachable.
		hid, hidErr := openHIDDevice(d)
		if hidErr == nil {
			return &DeviceHandle{
				device:           d,
				fileHandle:       windows.InvalidHandle,
				interfaceHandles: make(map[uint8]winusbInterfaceHandle),
				claimedIfaces:    make(map[uint8]bool),
				currentConfig:    1,
				hid:              hid,
			}, nil
		}
		if hidErr == ErrNotSupported {
			return nil, fmt.Errorf("device exposes only pointing or keyboard collections: %w", ErrNotSupported)
		}
		return nil, fmt.Errorf("failed to open device: %w", err)
	}

	// Initialize WinUSB
	var winusbHandle winusbInterfaceHandle
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_Initialize.Addr(),
		uintptr(fileHandle),
		uintptr(unsafe.Pointer(&winusbHandle)),
	)
	if r0 == 0 {
		windows.CloseHandle(fileHandle)

		// WinUSB is not this device's function driver. Try HID before giving up.
		if hid, hidErr := openHIDDevice(d); hidErr == nil {
			return &DeviceHandle{
				device:           d,
				fileHandle:       windows.InvalidHandle,
				interfaceHandles: make(map[uint8]winusbInterfaceHandle),
				claimedIfaces:    make(map[uint8]bool),
				currentConfig:    1,
				hid:              hid,
			}, nil
		}
		return nil, fmt.Errorf("WinUsb_Initialize failed: %w", e1)
	}

	return &DeviceHandle{
		device:           d,
		fileHandle:       fileHandle,
		winusbHandle:     winusbHandle,
		interfaceHandles: make(map[uint8]winusbInterfaceHandle),
		claimedIfaces:    make(map[uint8]bool),
		closed:           false,
		currentConfig:    1, // Windows typically uses config 1
	}, nil
}

// Close closes the device handle
// Close closes the device handle.
//
// Known gap, found while fixing the analogous real deadlock on Linux (see
// device_linux.go's reapLoop): an AsyncTransfer with a very long or
// disabled timeout (SetTimeout(0) or a large duration) that is still
// genuinely in flight when Close runs here is not cancelled first --
// unlike Linux, which now polls and always notices h.closed on its own,
// or macOS, where CFRunLoopStop unconditionally interrupts the run loop
// regardless of what is pending. Windows' AsyncTransfer.Submit goroutine
// is only bounded by its own timeout (5s by default, real and safe for
// that common case), so this is a real but narrow gap: it only matters if
// a caller opts into an unusually long wait and then closes concurrently.
// Not fixed here: AsyncTransfer.Cancel takes h.mu.RLock(), and this
// function holds h.mu.Lock() for its entire body, so calling Cancel from
// here directly would self-deadlock on Go's non-reentrant RWMutex --
// fixing this for real needs restructuring that locking, and verifying
// the fix against real Windows hardware, neither of which happened in
// this pass.
func (h *DeviceHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil
	}
	h.closed = true

	if h.hid != nil {
		return h.hid.close()
	}

	// Release all interfaces
	for iface := range h.interfaceHandles {
		h.releaseInterfaceInternal(iface)
	}

	// Free WinUSB handle
	if h.winusbHandle != 0 {
		syscall.SyscallN(procWinUsb_Free.Addr(), uintptr(h.winusbHandle))
		h.winusbHandle = 0
	}

	// Close file handle
	if h.fileHandle != windows.InvalidHandle {
		windows.CloseHandle(h.fileHandle)
		h.fileHandle = windows.InvalidHandle
	}

	return nil
}

// Descriptor returns the device descriptor
func (h *DeviceHandle) Descriptor() DeviceDescriptor {
	return h.device.Descriptor
}

// Device returns the underlying device
func (h *DeviceHandle) Device() *Device {
	return h.device
}

// SetConfiguration sets the device configuration
func (h *DeviceHandle) SetConfiguration(config int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	// WinUSB automatically selects configuration 1
	// Changing configuration requires re-initialization
	h.currentConfig = config
	return nil
}

// Configuration returns the current configuration
func (h *DeviceHandle) Configuration() (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	return h.currentConfig, nil
}

// ClaimInterface claims a USB interface
func (h *DeviceHandle) ClaimInterface(iface uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	if h.claimedIfaces[iface] {
		return nil // Already claimed
	}

	// Interface 0 is the default interface (winusbHandle)
	if iface == 0 {
		h.claimedIfaces[0] = true
		return nil
	}

	// Get associated interface for non-zero interfaces
	var ifaceHandle winusbInterfaceHandle
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetAssociatedInterface.Addr(),
		uintptr(h.winusbHandle),
		uintptr(iface-1), // WinUSB uses 0-based index for associated interfaces
		uintptr(unsafe.Pointer(&ifaceHandle)),
	)
	if r0 == 0 {
		return fmt.Errorf("WinUsb_GetAssociatedInterface failed: %w", e1)
	}

	h.interfaceHandles[iface] = ifaceHandle
	h.claimedIfaces[iface] = true
	return nil
}

// ReleaseInterface releases a claimed interface
func (h *DeviceHandle) ReleaseInterface(iface uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	return h.releaseInterfaceInternal(iface)
}

func (h *DeviceHandle) releaseInterfaceInternal(iface uint8) error {
	if !h.claimedIfaces[iface] {
		return nil
	}

	if ifaceHandle, ok := h.interfaceHandles[iface]; ok && ifaceHandle != 0 {
		syscall.SyscallN(procWinUsb_Free.Addr(), uintptr(ifaceHandle))
		delete(h.interfaceHandles, iface)
	}

	delete(h.claimedIfaces, iface)
	return nil
}

// SetInterfaceAltSetting sets the alternate setting for an interface
func (h *DeviceHandle) SetInterfaceAltSetting(iface uint8, altSetting uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	ifaceHandle := h.getInterfaceHandle(iface)
	if ifaceHandle == 0 {
		return fmt.Errorf("interface %d not claimed", iface)
	}

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_SetCurrentAlternateSetting.Addr(),
		uintptr(ifaceHandle),
		uintptr(altSetting),
	)
	if r0 == 0 {
		return fmt.Errorf("WinUsb_SetCurrentAlternateSetting failed: %w", e1)
	}

	return nil
}

// ClearHalt clears the halt condition on an endpoint
func (h *DeviceHandle) ClearHalt(endpoint uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	ifaceHdl := h.getInterfaceHandle(h.interfaceForEndpoint(endpoint))
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_ResetPipe.Addr(),
		uintptr(ifaceHdl),
		uintptr(endpoint),
	)
	if r0 == 0 {
		return fmt.Errorf("WinUsb_ResetPipe failed: %w", e1)
	}

	return nil
}

// DetachKernelDriver detaches the kernel driver from an interface.
//
// Windows binds a function driver at install time; there is no user-space way
// to unbind it, so this always reports ErrNotSupported rather than pretending
// to have detached anything. Compare Linux, where this really does issue
// USBDEVFS_DISCONNECT.
func (h *DeviceHandle) DetachKernelDriver(iface uint8) error {
	return ErrNotSupported
}

// AttachKernelDriver re-attaches the kernel driver to an interface.
//
// Rebinding a driver on Windows requires driver installation, so this always
// reports ErrNotSupported.
func (h *DeviceHandle) AttachKernelDriver(iface uint8) error {
	return ErrNotSupported
}

// StringDescriptor reads a string descriptor
func (h *DeviceHandle) StringDescriptor(index uint8) (string, error) {
	if index == 0 {
		return "", nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return "", ErrDeviceNotFound
	}

	buf := make([]byte, 256)
	var transferred uint32

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(USB_DT_STRING),
		uintptr(index),
		uintptr(0x0409), // English (US)
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return "", fmt.Errorf("WinUsb_GetDescriptor failed: %w", e1)
	}

	if transferred < 2 {
		return "", fmt.Errorf("invalid string descriptor")
	}

	// Parse USB string descriptor (UTF-16LE encoded)
	length := int(buf[0])
	if length > int(transferred) {
		length = int(transferred)
	}

	result := make([]uint16, 0, (length-2)/2)
	for i := 2; i < length; i += 2 {
		if i+1 < length {
			result = append(result, binary.LittleEndian.Uint16(buf[i:i+2]))
		}
	}

	return string(utf16ToRunes(result)), nil
}

// RawConfigDescriptor gets raw configuration descriptor data
func (h *DeviceHandle) RawConfigDescriptor(index uint8) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrDeviceNotFound
	}

	// First get just the header to find total length
	header := make([]byte, 9)
	var transferred uint32

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(USB_DT_CONFIG),
		uintptr(index),
		uintptr(0),
		uintptr(unsafe.Pointer(&header[0])),
		uintptr(len(header)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return nil, fmt.Errorf("WinUsb_GetDescriptor failed: %w", e1)
	}

	totalLength := binary.LittleEndian.Uint16(header[2:4])

	// Now get full descriptor
	fullBuf := make([]byte, totalLength)
	r0, _, e1 = syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(USB_DT_CONFIG),
		uintptr(index),
		uintptr(0),
		uintptr(unsafe.Pointer(&fullBuf[0])),
		uintptr(len(fullBuf)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return nil, fmt.Errorf("WinUsb_GetDescriptor failed: %w", e1)
	}

	return fullBuf[:transferred], nil
}

// ConfigDescriptorByValue gets parsed configuration descriptor by value
func (h *DeviceHandle) ConfigDescriptorByValue(index uint8) (*ConfigDescriptor, error) {
	data, err := h.RawConfigDescriptor(index - 1) // Convert 1-based to 0-based
	if err != nil {
		return nil, err
	}

	config := &ConfigDescriptor{}
	err = config.Unmarshal(data)
	if err != nil {
		return nil, err
	}
	return config, nil
}

// Speed gets the device speed
func (h *DeviceHandle) Speed() (uint8, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	var speed uint8
	var length uint32 = 1

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_QueryDeviceInformation.Addr(),
		uintptr(h.winusbHandle),
		uintptr(DEVICE_SPEED),
		uintptr(unsafe.Pointer(&length)),
		uintptr(unsafe.Pointer(&speed)),
	)
	if r0 == 0 {
		return 0, fmt.Errorf("WinUsb_QueryDeviceInformation failed: %w", e1)
	}

	return speed, nil
}

// ResetDevice performs a device reset
func (h *DeviceHandle) ResetDevice() error {
	// WinUSB doesn't directly support device reset
	// We need to close and reopen the device
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	// Store device reference
	device := h.device

	// Close current handles
	for iface := range h.interfaceHandles {
		h.releaseInterfaceInternal(iface)
	}
	if h.winusbHandle != 0 {
		syscall.SyscallN(procWinUsb_Free.Addr(), uintptr(h.winusbHandle))
	}
	if h.fileHandle != windows.InvalidHandle {
		windows.CloseHandle(h.fileHandle)
	}

	// Reopen the device
	pathPtr, err := windows.UTF16PtrFromString(device.devicePath)
	if err != nil {
		return fmt.Errorf("invalid device path: %w", err)
	}

	fileHandle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to reopen device: %w", err)
	}

	var winusbHandle winusbInterfaceHandle
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_Initialize.Addr(),
		uintptr(fileHandle),
		uintptr(unsafe.Pointer(&winusbHandle)),
	)
	if r0 == 0 {
		windows.CloseHandle(fileHandle)
		return fmt.Errorf("WinUsb_Initialize failed: %w", e1)
	}

	h.fileHandle = fileHandle
	h.winusbHandle = winusbHandle
	h.interfaceHandles = make(map[uint8]winusbInterfaceHandle)
	h.claimedIfaces = make(map[uint8]bool)

	return nil
}

// getInterfaceHandle returns the WinUSB handle for an interface
func (h *DeviceHandle) getInterfaceHandle(iface uint8) winusbInterfaceHandle {
	if iface == 0 {
		return h.winusbHandle
	}
	return h.interfaceHandles[iface]
}

// interfaceForEndpoint scans the stored raw configuration descriptors to find
// which interface number owns the given endpoint address. Returns 0 if the
// endpoint is on interface 0 or not found.
func (h *DeviceHandle) interfaceForEndpoint(endpoint uint8) uint8 {
	if h.device == nil {
		return 0
	}
	for _, raw := range h.device.rawConfigs {
		if iface, ok := endpointInterface(raw, endpoint); ok {
			return iface
		}
	}
	return 0
}

// endpointInterface scans a single raw configuration descriptor and returns
// the interface number that declares endpoint addr.
func endpointInterface(rawConfig []byte, addr uint8) (iface uint8, found bool) {
	if len(rawConfig) < 9 {
		return 0, false
	}
	pos := int(rawConfig[0]) // skip config descriptor (variable length)
	curIface := uint8(0)
	for pos+2 <= len(rawConfig) {
		length := int(rawConfig[pos])
		if length < 2 || pos+length > len(rawConfig) {
			break
		}
		switch rawConfig[pos+1] {
		case USB_DT_INTERFACE:
			if length >= 9 {
				curIface = rawConfig[pos+2]
			}
		case USB_DT_ENDPOINT:
			if length >= 3 && rawConfig[pos+2] == addr {
				return curIface, true
			}
		}
		pos += length
	}
	return 0, false
}

// SetPipePolicy sets a policy for a pipe
func (h *DeviceHandle) SetPipePolicy(endpoint uint8, policyType uint32, value uint32) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	ifaceHdl := h.getInterfaceHandle(h.interfaceForEndpoint(endpoint))
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_SetPipePolicy.Addr(),
		uintptr(ifaceHdl),
		uintptr(endpoint),
		uintptr(policyType),
		uintptr(4),
		uintptr(unsafe.Pointer(&value)),
	)
	if r0 == 0 {
		return fmt.Errorf("WinUsb_SetPipePolicy failed: %w", e1)
	}

	return nil
}

// SetTimeout sets the timeout for a pipe
func (h *DeviceHandle) SetTimeout(endpoint uint8, timeout time.Duration) error {
	ms := uint32(timeout.Milliseconds())
	return h.SetPipePolicy(endpoint, PIPE_TRANSFER_TIMEOUT, ms)
}

// Interface gets the alternate setting of an interface
func (h *DeviceHandle) Interface(iface uint8) (uint8, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	ifaceHandle := h.getInterfaceHandle(iface)
	if ifaceHandle == 0 {
		return 0, fmt.Errorf("interface %d not claimed", iface)
	}

	var altSetting uint8
	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetCurrentAlternateSetting.Addr(),
		uintptr(ifaceHandle),
		uintptr(unsafe.Pointer(&altSetting)),
	)
	if r0 == 0 {
		return 0, fmt.Errorf("WinUsb_GetCurrentAlternateSetting failed: %w", e1)
	}

	return altSetting, nil
}

// Status gets device, interface, or endpoint status
func (h *DeviceHandle) Status(requestType uint8, index uint16) (uint16, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	buf := make([]byte, 2)
	_, err := h.controlTransferInternal(requestType, USB_REQ_GET_STATUS, 0, index, buf, 5*time.Second)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint16(buf), nil
}

// controlTransferInternal is an internal helper that doesn't acquire locks
func (h *DeviceHandle) controlTransferInternal(requestType, request uint8, value, index uint16, data []byte, timeout time.Duration) (int, error) {
	packet := encodeSetupPacket(requestType, request, value, index, uint16(len(data)))

	var transferred uint32
	ok, err := winusbControlTransfer(h.winusbHandle, packet, data, &transferred, nil)
	if !ok {
		return 0, fmt.Errorf("WinUsb_ControlTransfer failed: %w", err)
	}

	return int(transferred), nil
}

// RawDescriptor gets any descriptor by type and index
func (h *DeviceHandle) RawDescriptor(descType uint8, descIndex uint8, langID uint16, data []byte) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}

	var dataPtr unsafe.Pointer
	if len(data) > 0 {
		dataPtr = unsafe.Pointer(&data[0])
	}

	var transferred uint32

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(descType),
		uintptr(descIndex),
		uintptr(langID),
		uintptr(dataPtr),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return 0, fmt.Errorf("WinUsb_GetDescriptor failed: %w", e1)
	}

	return int(transferred), nil
}

// SetFeature sets a feature on device, interface, or endpoint
func (h *DeviceHandle) SetFeature(requestType uint8, feature uint16, index uint16) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	_, err := h.controlTransferInternal(requestType, USB_REQ_SET_FEATURE, feature, index, nil, 5*time.Second)
	return err
}

// ClearFeature clears a feature on device, interface, or endpoint
func (h *DeviceHandle) ClearFeature(requestType uint8, feature uint16, index uint16) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return ErrDeviceNotFound
	}

	_, err := h.controlTransferInternal(requestType, USB_REQ_CLEAR_FEATURE, feature, index, nil, 5*time.Second)
	return err
}

// SetDescriptor issues a SET_DESCRIPTOR standard request.
func (h *DeviceHandle) SetDescriptor(descType uint8, descIndex uint8, langID uint16, data []byte) error {
	value := (uint16(descType) << 8) | uint16(descIndex)
	_, err := h.ControlTransfer(
		0x00, // Host-to-device, standard, device
		USB_REQ_SET_DESCRIPTOR,
		value,
		langID,
		data,
		5*time.Second,
	)
	return err
}

// SynchFrame issues a SYNCH_FRAME standard request for an isochronous endpoint.
func (h *DeviceHandle) SynchFrame(endpoint uint8) (uint16, error) {
	buf := make([]byte, 2)
	_, err := h.ControlTransfer(
		0x82, // Device-to-host, standard, endpoint
		USB_REQ_SYNCH_FRAME,
		0,
		uint16(endpoint),
		buf,
		5*time.Second,
	)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(buf), nil
}

// AllocStreams allocates bulk streams (USB 3.0+).
//
// WinUSB exposes no stream API, so this always reports ErrNotSupported.
func (h *DeviceHandle) AllocStreams(numStreams uint32, endpoints []uint8) error {
	return ErrNotSupported
}

// FreeStreams releases bulk streams (USB 3.0+).
//
// WinUSB exposes no stream API, so this always reports ErrNotSupported.
func (h *DeviceHandle) FreeStreams(endpoints []uint8) error {
	return ErrNotSupported
}

// Capabilities returns device capabilities (not available on Windows)
func (h *DeviceHandle) Capabilities() (uint32, error) {
	// Windows doesn't have direct equivalent to Linux usbfs capabilities
	return 0, ErrNotSupported
}

// USB20ExtensionDescriptor gets the USB 2.0 extension descriptor
func (h *DeviceHandle) USB20ExtensionDescriptor() (*USB2ExtensionCapability, error) {
	bos, caps, err := h.ReadBOSDescriptor()
	if err != nil {
		return nil, err
	}

	// Read full BOS to parse capabilities
	buf := make([]byte, bos.TotalLength)
	_, err = h.RawDescriptor(USB_DT_BOS, 0, 0, buf)
	if err != nil {
		return nil, err
	}

	pos := 5
	for i := 0; i < int(len(caps)) && pos < len(buf); i++ {
		if pos+3 > len(buf) {
			break
		}

		length := int(buf[pos])
		descType := buf[pos+1]
		devCapType := buf[pos+2]

		if length < 3 || pos+length > len(buf) {
			break
		}

		if descType == USB_DT_DEVICE_CAPABILITY && devCapType == 0x02 {
			if length < 7 {
				return nil, fmt.Errorf("invalid USB 2.0 extension capability length: %d", length)
			}
			return &USB2ExtensionCapability{
				Length:            buf[pos],
				DescriptorType:    buf[pos+1],
				DevCapabilityType: buf[pos+2],
				Attributes:        binary.LittleEndian.Uint32(buf[pos+3 : pos+7]),
			}, nil
		}

		pos += length
	}

	return nil, fmt.Errorf("USB 2.0 extension capability not found")
}

// SSUSBDeviceCapabilityDescriptor gets the SuperSpeed USB device capability descriptor
func (h *DeviceHandle) SSUSBDeviceCapabilityDescriptor() (*SuperSpeedUSBCapability, error) {
	bos, caps, err := h.ReadBOSDescriptor()
	if err != nil {
		return nil, err
	}

	// Read full BOS to parse capabilities
	buf := make([]byte, bos.TotalLength)
	_, err = h.RawDescriptor(USB_DT_BOS, 0, 0, buf)
	if err != nil {
		return nil, err
	}

	pos := 5
	for i := 0; i < int(len(caps)) && pos < len(buf); i++ {
		if pos+3 > len(buf) {
			break
		}

		length := int(buf[pos])
		descType := buf[pos+1]
		devCapType := buf[pos+2]

		if length < 3 || pos+length > len(buf) {
			break
		}

		if descType == USB_DT_DEVICE_CAPABILITY && devCapType == 0x03 {
			if length < 10 {
				return nil, fmt.Errorf("invalid SuperSpeed USB capability length: %d", length)
			}
			return &SuperSpeedUSBCapability{
				Length:                 buf[pos],
				DescriptorType:         buf[pos+1],
				DevCapabilityType:      buf[pos+2],
				Attributes:             buf[pos+3],
				SpeedsSupported:        binary.LittleEndian.Uint16(buf[pos+4 : pos+6]),
				FunctionalitySupported: buf[pos+6],
				U1DevExitLat:           buf[pos+7],
				U2DevExitLat:           binary.LittleEndian.Uint16(buf[pos+8 : pos+10]),
			}, nil
		}

		pos += length
	}

	return nil, fmt.Errorf("SuperSpeed USB capability not found")
}

// SSEndpointCompanionDescriptor gets the SuperSpeed endpoint companion descriptor
func (h *DeviceHandle) SSEndpointCompanionDescriptor(configIndex uint8, interfaceNumber uint8, altSetting uint8, endpointAddress uint8) (*SuperSpeedEndpointCompanionDescriptor, error) {
	config, err := h.ConfigDescriptorByValue(configIndex)
	if err != nil {
		return nil, err
	}

	altSettingDesc := config.InterfaceAltSetting(interfaceNumber, altSetting)
	if altSettingDesc == nil {
		return nil, fmt.Errorf("interface %d alt setting %d not found", interfaceNumber, altSetting)
	}

	for i := range altSettingDesc.Endpoints {
		if altSettingDesc.Endpoints[i].EndpointAddr == endpointAddress {
			if altSettingDesc.Endpoints[i].SSCompanion != nil {
				return altSettingDesc.Endpoints[i].SSCompanion, nil
			}
			extra := altSettingDesc.Endpoints[i].Extra
			pos := 0
			for pos+2 <= len(extra) {
				length := int(extra[pos])
				descType := extra[pos+1]

				if length < 2 || pos+length > len(extra) {
					break
				}

				if descType == USB_DT_SS_ENDPOINT_COMPANION {
					if length < 6 {
						return nil, fmt.Errorf("invalid SS endpoint companion descriptor length: %d", length)
					}
					return &SuperSpeedEndpointCompanionDescriptor{
						Length:           extra[pos],
						DescriptorType:   extra[pos+1],
						MaxBurst:         extra[pos+2],
						Attributes:       extra[pos+3],
						BytesPerInterval: binary.LittleEndian.Uint16(extra[pos+4 : pos+6]),
					}, nil
				}

				pos += length
			}
			return nil, fmt.Errorf("SS endpoint companion descriptor not found for endpoint %02x", endpointAddress)
		}
	}

	return nil, fmt.Errorf("endpoint %02x not found", endpointAddress)
}
