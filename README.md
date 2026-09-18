# github.com/tridentsx/go-usb

This is a fork of [github.com/kevmo314/go-usb](https://github.com/kevmo314/go-usb).

A cross-platform Go library for USB device communication, providing a
libusb-like interface, without cgo and without libusb.

## Goal

One API, `import "github.com/tridentsx/go-usb"` and nothing else, that talks
to real USB hardware on Linux, macOS and Windows through each OS's native
interface — usbfs, IOKit and WinUSB/SetupAPI respectively — with **no cgo,
no C toolchain, and no libusb dependency, on any platform**:

- **Pure Go everywhere.** Linux and Windows always were; macOS reaches IOKit
  through [purego](https://github.com/ebitengine/purego)'s
  `dlopen`/`dlsym`/`SyscallN` rather than cgo (see
  [#14](https://github.com/kevmo314/go-usb/issues/14)). `go build`, cross-
  compilation and `GOOS=darwin go build` from a Linux CI runner all work with
  no C compiler installed, ever.
- **One contract, enforced at compile time.** Every backend implements the
  same `DeviceHandleInterface` (see [`api_contract.go`](api_contract.go)).
  Adding a method to one platform without the others breaks the build
  everywhere rather than shipping an API that only compiles on the
  maintainer's laptop. Where a platform genuinely cannot perform an
  operation, it returns `ErrNotSupported` — never a `nil` error pretending
  the operation happened.
- **Verified against real hardware, not just compiled.** Real USB devices,
  not mocks: see the [go-usb-jig](https://github.com/tridentsx/go-usb-jig)
  companion repo's hardware-gated test suite for the bulk/interrupt/
  isochronous/control/stall coverage on macOS specifically, and the platform
  table below for what else has been exercised on real hardware per
  platform.

## Features

- Cross-platform support (Linux, macOS and Windows)
- Pure Go implementation everywhere, including macOS (no libusb dependency, no C toolchain)
- Device enumeration and management
- Control, bulk, interrupt, and isochronous transfers
- Synchronous and asynchronous transfer operations
- Thread-safe device operations
- Comprehensive error handling
- String descriptor support
- Configuration and interface management

## Installation

```bash
go get github.com/tridentsx/go-usb
```

## Requirements

- Linux, macOS or Windows operating system
- Go 1.25 or higher
- Appropriate permissions to access USB devices:
  - Linux: Typically requires root or udev rules
  - macOS: May require entitlements or running with elevated privileges

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    usb "github.com/tridentsx/go-usb"
)

func main() {
    // Get list of USB devices (no context needed!)
    devices, err := usb.DeviceList()
    if err != nil {
        log.Fatal(err)
    }

    // Print all devices
    for _, dev := range devices {
        fmt.Printf("Device: Bus %03d Address %03d VID:PID %04x:%04x\n",
            dev.Bus, dev.Address,
            dev.Descriptor.VendorID, dev.Descriptor.ProductID)
    }

    // Open a specific device by VID/PID
    handle, err := usb.OpenDevice(0x1234, 0x5678)
    if err != nil {
        log.Fatal(err)
    }
    defer handle.Close()

    // Perform operations with the device...
}
```

## API

The full, canonical surface is `DeviceHandleInterface` in
[`api_contract.go`](api_contract.go) — every backend implements it in full,
asserted at compile time, so this list is never out of date on any one
platform without the build failing on the others. Grouped by purpose:

| Group | Methods |
|---|---|
| Lifecycle and identity | `Close`, `Device`, `Descriptor` |
| Configuration | `SetConfiguration`, `GetConfiguration`, `Configuration` |
| Interfaces and alt settings | `ClaimInterface`, `ReleaseInterface`, `SetAltSetting`, `SetInterfaceAltSetting`, `Interface` |
| Endpoint and device state | `ClearHalt`, `ResetDevice`, `ResetEndpoint` |
| Kernel driver interaction | `KernelDriverActive`, `DetachKernelDriver`, `AttachKernelDriver` |
| Synchronous transfers | `ControlTransfer`, `BulkTransfer`, `BulkTransferWithOptions`, `InterruptTransfer`, `InterruptTransferWithRetry`, `IsochronousTransfer` |
| Descriptors | `StringDescriptor`, `RawDescriptor`, `SetDescriptor`, `RawConfigDescriptor`, `ConfigDescriptorByValue`, `ReadConfigDescriptor`, `GetConfigDescriptor`, `GetActiveConfigDescriptor`, `GetDeviceDescriptor`, `GetBOSDescriptor`, `ReadBOSDescriptor`, `GetDeviceQualifierDescriptor`, `ReadDeviceQualifierDescriptor`, `USB20ExtensionDescriptor`, `SSUSBDeviceCapabilityDescriptor`, `SSEndpointCompanionDescriptor` |
| Standard requests | `Status`, `GetStatus`, `SetFeature`, `ClearFeature`, `SynchFrame` |
| Capabilities and speed | `Speed`, `GetSpeed`, `Capabilities`, `GetCapabilities` |
| Bulk streams (USB 3.0) | `AllocStreams`, `FreeStreams` |
| Transfer objects | `SubmitTransfer`, `CancelTransfer`, `ReapTransfer`, `NewIsochronousTransfer` |

Package-level functions outside the per-handle contract: `DeviceList`,
`OpenDevice`, `OpenDeviceWithPath`, `IsValidDevicePath`, and
`RegisterHotplugCallback` for hotplug notifications (see the platform table
below for where each of these is actually implemented versus a documented
`ErrNotSupported`).

A few genuinely platform-specific escape hatches are deliberately kept out
of the portable contract rather than faked everywhere — see the note at the
bottom of `api_contract.go` for the exact list (a Linux usbfs file
descriptor, macOS's CFRunLoop-shaped async conveniences, Windows' WinUSB
pipe-policy calls).

## Usage Examples

### Enumerate Devices

```go
devices, _ := usb.DeviceList()
for _, dev := range devices {
    desc := dev.Descriptor
    fmt.Printf("VID: %04x, PID: %04x\n", desc.VendorID, desc.ProductID)
}
```

### Open Device and Read String Descriptors

```go
// Open device by VID/PID
handle, _ := usb.OpenDevice(vendorID, productID)
defer handle.Close()

// Or open a specific device from the list
devices, _ := usb.DeviceList()
handle, _ := devices[0].Open()
defer handle.Close()

// Get manufacturer string
manufacturer, _ := handle.StringDescriptor(desc.ManufacturerIndex)
fmt.Printf("Manufacturer: %s\n", manufacturer)

// Get product string
product, _ := handle.StringDescriptor(desc.ProductIndex)
fmt.Printf("Product: %s\n", product)
```

### Control Transfer

```go
// Read device descriptor
buf := make([]byte, 18)
n, err := handle.ControlTransfer(
    0x80,                    // bmRequestType (device-to-host)
    0x06,                    // bRequest (GET_DESCRIPTOR)
    0x0100,                  // wValue (DEVICE descriptor)
    0x0000,                  // wIndex
    buf,                     // data buffer
    5 * time.Second,         // timeout
)
```

### Bulk Transfer

```go
// Claim interface first
err := handle.ClaimInterface(0)
if err != nil {
    log.Fatal(err)
}
defer handle.ReleaseInterface(0)

// Write data to bulk endpoint
data := []byte("Hello USB!")
n, err := handle.BulkTransfer(
    0x02,                    // endpoint address (OUT endpoint 2)
    data,                    // data to send
    5 * time.Second,         // timeout
)

// Read data from bulk endpoint
buf := make([]byte, 512)
n, err = handle.BulkTransfer(
    0x82,                    // endpoint address (IN endpoint 2)
    buf,                     // buffer to receive data
    5 * time.Second,         // timeout
)
```

### Interrupt Transfer

```go
// Read from interrupt endpoint
buf := make([]byte, 64)
n, err := handle.InterruptTransfer(
    0x81,                    // endpoint address (IN endpoint 1)
    buf,                     // buffer to receive data
    100 * time.Millisecond,  // timeout
)
```

### Asynchronous Transfer

```go
// Create a transfer object
transfer := usb.NewTransfer(handle, 0x81, usb.TransferTypeInterrupt, 64)

// Set callback
transfer.SetCallback(func(t *usb.Transfer) {
    if t.Status() == usb.TransferCompleted {
        data := t.Buffer()
        fmt.Printf("Received %d bytes\n", t.ActualLength())
    }
})

// Submit transfer
err := handle.SubmitTransfer(transfer)

// Reap completed transfers
completedTransfer, err := handle.ReapTransfer(time.Second)
```

## Permissions

USB device access typically requires elevated privileges.

### Linux

#### Run as root
```bash
sudo go run main.go
```

#### Create udev rules
Create a file `/etc/udev/rules.d/99-usb.rules`:
```
# Allow access to specific device
SUBSYSTEM=="usb", ATTRS{idVendor}=="1234", ATTRS{idProduct}=="5678", MODE="0666"

# Allow access to all USB devices (less secure)
SUBSYSTEM=="usb", MODE="0666"
```

Then reload udev:
```bash
sudo udevadm control --reload-rules
sudo udevadm trigger
```

### macOS

#### Run with elevated privileges
```bash
sudo go run main.go
```

#### Code signing and entitlements
For distribution, your application may need:
- Code signing with a valid Developer ID
- USB entitlements in your app's Info.plist
- User approval in System Settings > Privacy & Security

## Included Tools

The repository includes several command-line tools and examples in the `cmd/` directory:

- **lsusb**: List USB devices (similar to the system lsusb command)
  ```bash
  go run cmd/lsusb/main.go       # List all devices
  go run cmd/lsusb/main.go -v     # Verbose output
  go run cmd/lsusb/main.go -t     # Tree view
  go run cmd/lsusb/main.go -s :6  # Show device 6 on any bus
  go run cmd/lsusb/main.go -s 1:6 # Show device 6 on bus 1
  ```

- **browse-msc**: Browse USB Mass Storage devices
- **browse-uvc**: Browse USB Video Class devices
- **verify-transfers**: Test and verify USB transfer operations

## Platform Support Matrix

See [API](#api) for what "the same API" means precisely. This table is
where the platforms actually differ — where one returns `ErrNotSupported`,
or the underlying mechanism is worth knowing about.

| Capability | Linux | macOS | Windows |
|---|---|---|---|
| Device enumeration | sysfs | IOKit | SetupAPI + hub IOCTLs (all devices) |
| Control / bulk / interrupt transfers | yes | yes | yes |
| Isochronous transfers | yes (usbfs URBs) | yes (IOKit) | `ErrNotSupported` |
| Asynchronous transfers | yes (`AsyncTransfer`) | yes (`AsyncTransfer`) | not yet |
| `Transfer.Submit` / `CancelTransfer` / `ReapTransfer` | `ErrNotSupported`, use `AsyncTransfer` | yes | `ErrNotSupported` |
| Bulk streams (`AllocStreams`) | yes | `ErrNotSupported` | `ErrNotSupported` |
| `DetachKernelDriver` / `AttachKernelDriver` | yes (`USBDEVFS_DISCONNECT`) | `ErrNotSupported` | `ErrNotSupported` |
| HID-class devices | raw, after detaching `usbhid` | not yet | report-level via `hid.dll` |
| `SetShortPacketMode`, `SubmitHighBandwidthIso` | yes | `ErrNotSupported` | `ErrNotSupported` |
| `Capabilities` | usbfs capability bits | `ErrNotSupported` | `ErrNotSupported` |
| Hotplug notifications (`RegisterHotplugCallback`) | not yet | yes (IOKit, verified against a real unplug/replug) | not yet |

### Opening devices on Windows

`DeviceList` reports every USB device, matching Linux and macOS. Descriptors,
cached strings, bus address and speed come from the hub each device is attached
to, so they are available whatever driver owns the device.

Opening one for I/O is a separate matter: WinUSB must be the device's function
driver. A device owned by another driver is fully described but `Device.Open`
will fail. Use a tool such as Zadig, or ship an INF, to bind WinUSB to the
device you intend to talk to.

Bus numbers are synthetic. Windows has no notion of a USB bus number, so root
hubs are numbered in a stable order; the value does not correspond to anything
the OS reports. Device addresses are the real addresses the hub reports.

For a composite device, `Device.Path` identifies the device while opening
targets its WinUSB function interface. Only the first such function is used, so
a device exposing several WinUSB functions is currently reachable through one of
them.

### The macOS backend is cgo-free

macOS reaches IOKit through [purego](https://github.com/ebitengine/purego)'s
`dlopen`/`dlsym`/`SyscallN`/`NewCallback` rather than cgo. `CGO_ENABLED=1` and
`CGO_ENABLED=0` build the identical set of files — there is no second,
cgo-based backend to choose between anymore (see #14: PRs #15-#22 built the
purego backend up to parity, and #25 removed the cgo backend it replaced).

This is why `GOOS=darwin CGO_ENABLED=0 go build ./...` works from Linux: the
macOS code can be cross-compiled and type-checked from any platform, with no
C toolchain needed anywhere, ever.

Device enumeration, opening, claiming interfaces, control/bulk/interrupt/
isochronous transfers (synchronous and asynchronous), hotplug and endpoint
stall/clear-halt are all implemented and verified end to end against real
hardware — see the [go-usb-jig](https://github.com/tridentsx/go-usb-jig)
companion repo's hardware-gated test suite for the bulk/interrupt/
isochronous/control/stall coverage specifically.

### Accessing HID devices

A lot of test equipment presents itself as a standard HID device so that it
needs no driver installation: some report measurements directly in input
reports, others tunnel a serial protocol over reports. Support differs by
platform.

On **Windows** such a device is opened through `hid.dll` automatically. `Open`
tries WinUSB first and falls back to the HID transport, so no caller change is
needed. What works:

- `InterruptTransfer` on the endpoints the configuration descriptor advertises,
  routed to the HID collection that serves them. Reports pass through unchanged,
  including the leading report ID, which matches `hidraw` on Linux.
- `GetFeatureReport`, `SetFeatureReport`, `GetInputReport`, `SetOutputReport`
  for HID class requests, and `FlushHIDQueue` to discard stale queued reports.
- `HIDReportLengths` reports the fixed report sizes, which a caller framing a
  protocol over reports needs in order to size buffers.
- `IsHID` tells you which transport a handle is using.

What cannot work: vendor-specific control transfers, bulk transfers and
isochronous transfers all return `ErrNotSupported`, because `hidclass.sys` owns
the control endpoint and will not carry them. If a device needs vendor control
requests, bind it to WinUSB instead.

Pointing devices and keyboards are deliberately excluded. Windows refuses read
and write access to them anyway, and a USB library is the wrong place to read
keystrokes from. Collections on usage page `0x01` with usage Pointer, Mouse,
Keyboard or Keypad, and all digitisers on page `0x0D`, are skipped. Consumer
control (page `0x0C`) is allowed, since monitors and instruments use it as a
control channel.

On **Linux** you can call `DetachKernelDriver` to unbind `usbhid` and then use
raw transfers, which needs root or a udev rule. That is more capable than the
Windows HID path, since it carries vendor control requests too.

On **macOS** HID devices enumerate but cannot currently be opened: `IOHIDFamily`
owns them and the backend has no IOHIDManager transport yet.

### `Device.Path` is platform-specific

`Path` is whatever string that platform uses to identify the device, and it is
not portable:

- Linux: the usbfs node, `/dev/bus/usb/001/002`
- macOS: an IOKit location, `iokit:14200000`
- Windows: a device interface path,
  `\\?\usb#vid_046d&pid_08e5#6&2e5a5a55&0&4#{a5dcbf10-...}`

On Windows this is not the string Device Manager shows. Device Manager displays
the *device instance path* (`USB\VID_046D&PID_08E5\6&2E5A5A55&0&4`); the
interface path is that value lowercased, with `\` replaced by `#`, plus the
interface class GUID.

Use `IsValidDevicePath` to check a path for the current platform.

### Hotplug is macOS-only for now

`RegisterHotplugCallback` is real on macOS — verified against a live
unplug/replug of a real hub, see [#20](https://github.com/kevmo314/go-usb/pull/20).
Linux and Windows report `ErrNotSupported`; poll `DeviceList` there if you
need to detect changes until their native mechanisms (netlink/udev,
`RegisterDeviceNotification`) land.

## Limitations

- Requires appropriate permissions for USB device access
- Hotplug notifications work on macOS; Linux and Windows are tracked but not
  implemented yet (see the platform table above)
- The Windows backend is newer than the Linux and macOS ones; isochronous and
  asynchronous transfers are not implemented there yet

## Resources

- [USB 2.0 Specification](https://www.usb.org/document-library/usb-20-specification)
- [Linux usbfs Documentation](https://www.kernel.org/doc/html/latest/driver-api/usb/index.html)
- [macOS IOKit USB Documentation](https://developer.apple.com/documentation/iokit)
- [libusb Documentation](https://libusb.info/)
