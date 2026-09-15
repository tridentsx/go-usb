//go:build linux || darwin

package usb

import "time"

// This file extends the canonical contract in api_contract.go to the
// asynchronous and isochronous transfer objects.
//
// It is restricted to Linux and macOS because the Windows backend has no
// AsyncTransfer type yet; its asynchronous and isochronous support is tracked
// separately. When that lands, these assertions should move into
// api_contract.go so all three platforms are held to them.

// AsyncTransferInterface is the portable contract for an asynchronous transfer.
type AsyncTransferInterface interface {
	Submit() error
	Wait() error
	WaitWithTimeout(timeout time.Duration) error
	Cancel() error
	IsCompleted() bool
	Status() TransferStatus
	ActualLength() int
	Buffer() []byte
	SetTimeout(timeout time.Duration)
	Fill(data []byte) error
}

// IsochronousTransferInterface is the portable contract for an isochronous
// transfer.
type IsochronousTransferInterface interface {
	Submit() error
	Wait() error
	Cancel() error
	Status() TransferStatus
	ActualLength() int
	Buffer() []byte
	Packets() []IsoPacketDescriptor
	IsoPacketBuffer(packetIndex int) ([]byte, error)
	IsoPacketBufferSlices() [][]byte
}

var (
	_ AsyncTransferInterface       = (*AsyncTransfer)(nil)
	_ IsochronousTransferInterface = (*IsochronousTransfer)(nil)
)

// Retained as platform-specific extras, outside the contract:
//
//   - Linux: AsyncTransfer.IsoPackets and SetIsoPacketLengths, which expose the
//     usbfs iso packet array, and IsochronousTransfer.RawStatus.
//   - macOS: IsochronousTransfer.SetCallback, SetUserData, GetUserData,
//     SetPacketLength and the GetPacket* accessors, which are shaped by IOKit.
