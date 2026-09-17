package usb

import (
	"encoding/binary"

	"golang.org/x/sys/windows"
)

// Hub IOCTLs let us read a device's descriptors and its bus address from the
// hub it is attached to, without opening the device itself. That works for
// every device regardless of which function driver owns it, so enumeration is
// not limited to WinUSB-bound devices.

var (
	ioctlUSBGetNodeInformation              = usbCtlCode(usbGetNodeInformation)
	ioctlUSBGetDescriptorFromNodeConnection = usbCtlCode(usbGetDescriptorFromNodeConnection)
	ioctlUSBGetNodeConnectionInformationEx  = usbCtlCode(usbGetNodeConnectionInformationEx)

	// ioctlUSBGetRootHubName is HCD_GET_ROOT_HUB_NAME (258), which coincidentally
	// has the same function-code value as USB_GET_NODE_INFORMATION. The kernel
	// dispatches the IOCTL differently depending on whether the target is a host
	// controller (xhci.sys/ehci.sys: returns USB_ROOT_HUB_NAME) or a hub
	// (usbhub.sys: returns USB_NODE_INFORMATION).
	ioctlUSBGetRootHubName = ioctlUSBGetNodeInformation
)

// maxPipesPerReply bounds the trailing USB_PIPE_INFO array we make room for.
const maxPipesPerReply = 32

// openHubForQuery opens a hub interface for IOCTL use.
//
// GENERIC_WRITE with FILE_SHARE_WRITE matches what USBView uses. Read access
// is deliberately not requested: the hub answers these queries without it, and
// asking for less keeps us from contending with other openers.
func openHubForQuery(hubPath string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(hubPath)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(p,
		windows.GENERIC_WRITE, windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
}

// hubPortCount returns how many downstream ports a hub has.
func hubPortCount(hub windows.Handle) (int, error) {
	// USB_NODE_INFORMATION: USB_HUB_NODE(4) then USB_HUB_DESCRIPTOR, whose
	// bNumberOfPorts is its third byte.
	buf := make([]byte, 96)
	var returned uint32
	if err := windows.DeviceIoControl(hub, ioctlUSBGetNodeInformation,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &returned, nil); err != nil {
		return 0, err
	}
	if returned < 7 {
		return 0, ErrIO
	}
	return int(buf[6]), nil
}

// hubNodeConnection asks a hub about the device on one of its ports.
func hubNodeConnection(hub windows.Handle, port int) (nodeConnection, error) {
	buf := encodeNodeConnectionRequest(uint32(port), maxPipesPerReply)
	var returned uint32
	if err := windows.DeviceIoControl(hub, ioctlUSBGetNodeConnectionInformationEx,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &returned, nil); err != nil {
		return nodeConnection{}, err
	}

	nc, ok := parseNodeConnection(buf[:returned])
	if !ok {
		return nodeConnection{}, ErrIO
	}
	return nc, nil
}

// hubDescriptor reads a descriptor for the device on a port straight from the
// hub's cache, without opening the device.
func hubDescriptor(hub windows.Handle, port int, descType, descIndex uint8, langID uint16, dataLen int) ([]byte, error) {
	buf := encodeDescriptorRequest(uint32(port), descType, descIndex, langID, dataLen)
	var returned uint32
	if err := windows.DeviceIoControl(hub, ioctlUSBGetDescriptorFromNodeConnection,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &returned, nil); err != nil {
		return nil, err
	}

	desc := parseDescriptorReply(buf, returned)
	if desc == nil {
		return nil, ErrIO
	}
	return desc, nil
}

// rootHubName returns the symbolic link name of the root hub attached to the
// given host controller handle, without the leading "\\?\" prefix.
//
// The host controller is opened by the caller; this function does not close it.
// The returned name can be made into an openable path by prepending `\\?\`.
func rootHubName(hc windows.Handle) (string, error) {
	// USB_ROOT_HUB_NAME: a 4-byte ActualLength followed by a variable-length
	// UTF-16LE string. Call once with a small buffer to learn the required size,
	// then again with a correctly-sized buffer.
	var header [4]byte
	var returned uint32
	windows.DeviceIoControl(hc, ioctlUSBGetRootHubName,
		nil, 0, &header[0], uint32(len(header)), &returned, nil)

	needed := binary.LittleEndian.Uint32(header[:])
	if needed <= 4 {
		return "", ErrIO
	}

	buf := make([]byte, needed)
	if err := windows.DeviceIoControl(hc, ioctlUSBGetRootHubName,
		nil, 0, &buf[0], uint32(len(buf)), &returned, nil); err != nil {
		return "", err
	}
	if returned < 4 {
		return "", ErrIO
	}

	// Name starts at byte 4 as UTF-16LE, null-terminated.
	nameBytes := buf[4:returned]
	nWords := len(nameBytes) / 2
	u16 := make([]uint16, nWords)
	for i := range nWords {
		u16[i] = binary.LittleEndian.Uint16(nameBytes[i*2:])
	}
	return windows.UTF16ToString(u16), nil
}

// hubStringDescriptor reads and decodes a string descriptor for a port.
func hubStringDescriptor(hub windows.Handle, port int, index uint8) string {
	if index == 0 {
		return ""
	}
	desc, err := hubDescriptor(hub, port, USB_DT_STRING, index, 0x0409, 255)
	if err != nil {
		return ""
	}
	return decodeStringDescriptor(desc)
}

// hubConfigDescriptor reads a configuration descriptor for a port, following
// up with a second read once the total length is known.
func hubConfigDescriptor(hub windows.Handle, port int, index uint8) ([]byte, error) {
	header, err := hubDescriptor(hub, port, USB_DT_CONFIG, index, 0, 9)
	if err != nil {
		return nil, err
	}
	if len(header) < 4 {
		return nil, ErrIO
	}

	total := int(header[2]) | int(header[3])<<8
	if total <= len(header) {
		return header, nil
	}
	return hubDescriptor(hub, port, USB_DT_CONFIG, index, 0, total)
}
