// Package usb provides cross-platform USB device access for Linux, macOS
// and Windows, with the same Go API on every platform and no cgo anywhere
// (Linux and Windows never needed it; macOS reaches IOKit through
// [github.com/ebitengine/purego] instead of a C bridge).
//
// # Quick start
//
// List devices, open one, and read its manufacturer string:
//
//	devices, err := usb.DeviceList()
//	if err != nil {
//		log.Fatal(err)
//	}
//	for _, dev := range devices {
//		fmt.Printf("%04x:%04x\n", dev.Descriptor.VendorID, dev.Descriptor.ProductID)
//	}
//
//	handle, err := usb.OpenDevice(0x1234, 0x5678)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer handle.Close()
//
//	name, err := handle.StringDescriptor(handle.Descriptor().ManufacturerIndex)
//
// See the Example functions throughout this package for a runnable version
// of every capability below, and the [DeviceHandleInterface] in
// api_contract.go for the complete, compile-time-enforced method set every
// platform backend implements identically.
//
// # Transfers
//
// Four transfer types, each with a synchronous and an asynchronous form:
//
//   - Control: [DeviceHandle.ControlTransfer].
//   - Bulk: [DeviceHandle.BulkTransfer] (synchronous), [DeviceHandle.NewBulkTransfer]
//     (asynchronous, returns an [AsyncTransfer]).
//   - Interrupt: [DeviceHandle.InterruptTransfer], [DeviceHandle.NewInterruptTransfer].
//   - Isochronous: [DeviceHandle.NewIsochronousTransfer], which returns an
//     [IsochronousTransfer] — there is no synchronous form, since isochronous
//     transfer is inherently a streaming, multi-packet operation.
//
// [AsyncTransfer] is the one real asynchronous API: create one with
// [DeviceHandle.NewBulkTransfer], [DeviceHandle.NewInterruptTransfer] or
// [DeviceHandle.NewControlTransfer], queue it with [AsyncTransfer.Submit],
// and either poll [AsyncTransfer.Wait]/[AsyncTransfer.WaitWithTimeout] or set
// a callback with [Transfer.SetCallback] before submitting. An older,
// separate family — [NewTransfer], [Transfer.Submit], [Transfer.Cancel],
// [DeviceHandle.SubmitTransfer], [DeviceHandle.CancelTransfer],
// [DeviceHandle.ReapTransfer] — is deprecated: it reports ErrNotSupported on
// Linux and Windows, and even on macOS, the one platform where Submit does
// something, it blocks synchronously rather than actually queuing anything,
// and ReapTransfer is unconditionally unimplemented. Use AsyncTransfer.
//
// # Platform differences
//
// Every method in [DeviceHandleInterface] exists and type-checks on every
// platform, asserted at compile time — but a handful of operations are only
// really implemented on some of them, and report ErrNotSupported on the
// rest rather than silently doing nothing. The main ones:
//
//   - Bulk streams ([DeviceHandle.AllocStreams]): Linux only.
//   - [DeviceHandle.DetachKernelDriver]/[DeviceHandle.AttachKernelDriver]: Linux only.
//   - HID report access ([DeviceHandle.IsHID], [DeviceHandle.GetFeatureReport], etc.):
//     macOS and Windows, each through a different platform HID stack; Linux
//     reaches the same devices through [DeviceHandle.DetachKernelDriver] plus raw
//     transfers instead, which is more capable (it can carry vendor control
//     requests) but requires root or a udev rule.
//   - [RegisterHotplugCallback]: implemented on all three, but only verified
//     against real hardware on macOS and Windows so far.
//
// See the README (https://github.com/tridentsx/go-usb) for the full
// platform support matrix and the reasoning behind each of these, including
// which ones are fundamentally platform-specific versus simply not
// implemented yet.
package usb
