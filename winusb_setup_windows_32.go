//go:build windows && (386 || arm)

package usb

import "encoding/binary"

// setupPacketArgs converts a control setup packet into the syscall arguments
// that pass it *by value*.
//
// WinUsb_ControlTransfer's second parameter is a WINUSB_SETUP_PACKET, not a
// pointer to one. On 32-bit Windows an 8-byte structure passed by value
// occupies two stack slots, so it becomes two uintptr arguments in
// little-endian order.
func setupPacketArgs(p [setupPacketSize]byte) []uintptr {
	return []uintptr{
		uintptr(binary.LittleEndian.Uint32(p[0:4])),
		uintptr(binary.LittleEndian.Uint32(p[4:8])),
	}
}
