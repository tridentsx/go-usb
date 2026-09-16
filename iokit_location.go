package usb

import "fmt"

// IOKit locationID handling, kept untagged so it is compiled and unit-tested on
// every host rather than only on macOS.
//
// A locationID is a 32-bit value the USB host controller assigns. The high byte
// identifies the controller, and each following nibble is a port number one
// level further down the tree, zero-padded on the right:
//
//	0x14200000  ->  controller 0x14, port 2, then port 2 of that hub
//
// It is the closest thing macOS has to a bus number plus port path.

// busFromLocationID derives a bus number from a locationID.
//
// The high byte is the controller, which is the nearest equivalent of the bus
// number Linux reports. It is not a small counting number: real values look
// like 0x14 or 0xfa.
func busFromLocationID(locationID uint32) uint8 {
	return uint8(locationID >> 24)
}

// portChainFromLocationID returns the port numbers from the controller down to
// the device, which is the path a device occupies on the tree.
func portChainFromLocationID(locationID uint32) []uint8 {
	var chain []uint8
	// Walk the six nibbles below the controller byte, stopping at the first
	// zero: a zero nibble means the chain has ended.
	for shift := 20; shift >= 0; shift -= 4 {
		nibble := uint8((locationID >> uint(shift)) & 0xf)
		if nibble == 0 {
			break
		}
		chain = append(chain, nibble)
	}
	return chain
}

// iokitDevicePath formats the Path value for a macOS device.
//
// The format is shared with the cgo backend so that a caller cannot tell the two
// apart, and so IsValidDevicePath accepts both.
func iokitDevicePath(locationID uint32) string {
	return fmt.Sprintf("iokit:%08x", locationID)
}
