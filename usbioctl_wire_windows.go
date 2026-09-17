package usb

import "encoding/binary"

// This file holds the wire format of the Windows USB hub IOCTLs.
//
// Every structure here is ONE-BYTE PACKED. usbioctl.h wraps them in
// pshpack1.h, so a Go struct with natural alignment silently reads the wrong
// fields: USHORT DeviceAddress lands at offset 25, not 26, and the following
// ULONGs shift with it. These helpers therefore read at explicit offsets.

// Windows I/O control code construction, from CTL_CODE in winioctl.h.
const (
	fileDeviceUSB  = 0x22
	methodBuffered = 0
	fileAnyAccess  = 0
)

// usbCtlCode builds a FILE_DEVICE_USB control code for the given function.
func usbCtlCode(function uint32) uint32 {
	return fileDeviceUSB<<16 | fileAnyAccess<<14 | function<<2 | methodBuffered
}

// USB hub IOCTL function numbers from usbioctl.h.
const (
	usbGetNodeInformation               = 258
	usbGetNodeConnectionInformation     = 259
	usbGetDescriptorFromNodeConnection  = 260
	usbGetNodeConnectionName            = 261
	usbGetNodeConnectionDriverKeyName   = 264
	usbGetNodeConnectionInformationEx   = 274
	usbGetNodeConnectionInformationExV2 = 279
)

// nodeConnectionInfoSize is the size of USB_NODE_CONNECTION_INFORMATION_EX
// without its trailing pipe array. Each USB_PIPE_INFO adds 11 bytes.
const nodeConnectionInfoSize = 35

// Offsets within USB_NODE_CONNECTION_INFORMATION_EX, one-byte packed.
const (
	nciConnectionIndex    = 0  // ULONG
	nciDeviceDescriptor   = 4  // USB_DEVICE_DESCRIPTOR, 18 bytes
	nciCurrentConfigValue = 22 // UCHAR
	nciSpeed              = 23 // UCHAR
	nciDeviceIsHub        = 24 // BOOLEAN
	nciDeviceAddress      = 25 // USHORT, no padding before it
	nciNumberOfOpenPipes  = 27 // ULONG
	nciConnectionStatus   = 31 // USB_CONNECTION_STATUS
)

// USB_CONNECTION_STATUS values.
const (
	usbNoDeviceConnected = 0
	usbDeviceConnected   = 1
)

// USB_DEVICE_SPEED values, as reported by the hub.
const (
	usbLowSpeed   = 0
	usbFullSpeed  = 1
	usbHighSpeed  = 2
	usbSuperSpeed = 3
)

// nodeConnection describes what a hub reports about one of its ports.
type nodeConnection struct {
	Connected     bool
	DeviceIsHub   bool
	DeviceAddress uint16
	Speed         uint8
	ConfigValue   uint8
	Descriptor    DeviceDescriptor
}

// encodeNodeConnectionRequest builds the input buffer for
// IOCTL_USB_GET_NODE_CONNECTION_INFORMATION_EX. The buffer doubles as the
// output buffer, so it is sized for the reply including a pipe array.
func encodeNodeConnectionRequest(port uint32, maxPipes int) []byte {
	buf := make([]byte, nodeConnectionInfoSize+maxPipes*11)
	binary.LittleEndian.PutUint32(buf[nciConnectionIndex:], port)
	return buf
}

// parseNodeConnection decodes a hub's reply about one port.
func parseNodeConnection(buf []byte) (nodeConnection, bool) {
	if len(buf) < nodeConnectionInfoSize {
		return nodeConnection{}, false
	}

	nc := nodeConnection{
		Connected:     binary.LittleEndian.Uint32(buf[nciConnectionStatus:]) == usbDeviceConnected,
		DeviceIsHub:   buf[nciDeviceIsHub] != 0,
		DeviceAddress: binary.LittleEndian.Uint16(buf[nciDeviceAddress:]),
		Speed:         buf[nciSpeed],
		ConfigValue:   buf[nciCurrentConfigValue],
	}
	nc.Descriptor = parseDeviceDescriptor(buf[nciDeviceDescriptor : nciDeviceDescriptor+18])
	return nc, true
}

// parseDeviceDescriptor decodes an 18-byte USB device descriptor.
func parseDeviceDescriptor(b []byte) DeviceDescriptor {
	if len(b) < 18 {
		return DeviceDescriptor{}
	}
	return DeviceDescriptor{
		Length:            b[0],
		DescriptorType:    b[1],
		USBVersion:        binary.LittleEndian.Uint16(b[2:4]),
		DeviceClass:       b[4],
		DeviceSubClass:    b[5],
		DeviceProtocol:    b[6],
		MaxPacketSize0:    b[7],
		VendorID:          binary.LittleEndian.Uint16(b[8:10]),
		ProductID:         binary.LittleEndian.Uint16(b[10:12]),
		DeviceVersion:     binary.LittleEndian.Uint16(b[12:14]),
		ManufacturerIndex: b[14],
		ProductIndex:      b[15],
		SerialNumberIndex: b[16],
		NumConfigurations: b[17],
	}
}

// descriptorRequestHeaderSize is the size of USB_DESCRIPTOR_REQUEST before its
// data area: ULONG ConnectionIndex plus an 8-byte setup packet.
const descriptorRequestHeaderSize = 12

// encodeDescriptorRequest builds the buffer for
// IOCTL_USB_GET_DESCRIPTOR_FROM_NODE_CONNECTION, which carries a standard USB
// setup packet and returns the descriptor in the same buffer after the header.
func encodeDescriptorRequest(port uint32, descType, descIndex uint8, langID uint16, dataLen int) []byte {
	buf := make([]byte, descriptorRequestHeaderSize+dataLen)
	binary.LittleEndian.PutUint32(buf[0:4], port)
	setup := encodeSetupPacket(0x80, USB_REQ_GET_DESCRIPTOR,
		descriptorRequestValue(descType, descIndex), langID, uint16(dataLen))
	copy(buf[4:descriptorRequestHeaderSize], setup[:])
	return buf
}

// parseDescriptorReply returns the descriptor bytes from a reply buffer,
// given how many bytes the driver reported writing.
func parseDescriptorReply(buf []byte, returned uint32) []byte {
	if returned <= descriptorRequestHeaderSize || int(returned) > len(buf) {
		return nil
	}
	return buf[descriptorRequestHeaderSize:returned]
}

// speedToSpeed maps a hub-reported USB_DEVICE_SPEED onto the portable Speed
// type. SuperSpeed+ needs the V2 connection IOCTL and is reported as
// SpeedSuper here.
func speedToSpeed(s uint8) Speed {
	switch s {
	case usbLowSpeed:
		return SpeedLow
	case usbFullSpeed:
		return SpeedFull
	case usbHighSpeed:
		return SpeedHigh
	case usbSuperSpeed:
		return SpeedSuper
	default:
		return SpeedUnknown
	}
}
