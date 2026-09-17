//go:build linux || darwin || windows

package usb

// Compile-time conformance assertions for the async and isochronous transfer
// interfaces. All three platforms must satisfy both interfaces.
var (
	_ AsyncTransferInterface       = (*AsyncTransfer)(nil)
	_ IsochronousTransferInterface = (*IsochronousTransfer)(nil)
)
