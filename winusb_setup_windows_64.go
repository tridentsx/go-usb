//go:build windows && (amd64 || arm64)

package usb

import "encoding/binary"

// setupPacketArgs converts a control setup packet into the syscall arguments
// that pass it *by value*.
//
// WinUsb_ControlTransfer's second parameter is a WINUSB_SETUP_PACKET, not a
// pointer to one. On 64-bit Windows the whole 8-byte structure travels in a
// single integer register, so it becomes exactly one uintptr argument.
func setupPacketArgs(p [setupPacketSize]byte) []uintptr {
	return []uintptr{uintptr(binary.LittleEndian.Uint64(p[:]))}
}
