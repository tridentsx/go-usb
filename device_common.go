package usb

// controlSetupPacket represents a USB control setup packet
type controlSetupPacket struct {
	bmRequestType uint8
	bRequest      uint8
	wValue        uint16
	wIndex        uint16
	wLength       uint16
}

// The cross-platform DeviceHandle contract lives in api_contract.go, where it
// is also asserted at compile time against every backend.
