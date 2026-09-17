//go:build linux || darwin

package usb

// Retained as platform-specific extras that live outside the portable
// AsyncTransferInterface / IsochronousTransferInterface contract:
//
//   - Linux: AsyncTransfer.IsoPackets and SetIsoPacketLengths expose the usbfs
//     iso packet array; IsochronousTransfer.RawStatus exposes the raw URB
//     status integer.
//   - macOS: IsochronousTransfer.SetCallback, SetUserData, GetUserData,
//     SetPacketLength and the GetPacket* accessors are shaped by IOKit and have
//     no counterpart on other platforms.
