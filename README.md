# github.com/kevmo314/go-usb

A cross-platform Go library for USB device communication, providing a libusb-like interface. On Linux, it uses the kernel's usbfs interface directly. On macOS, it uses the native IOKit framework.

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
go get github.com/kevmo314/go-usb
```

## Requirements

- Linux, macOS or Windows operating system
- Go 1.21 or higher
- Appropriate permissions to access USB devices:
  - Linux: Typically requires root or udev rules
  - macOS: May require entitlements or running with elevated privileges

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    usb "github.com/kevmo314/go-usb"
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

Every backend implements the same API. Where a platform cannot perform an
operation it returns `ErrNotSupported` rather than a `nil` error, so a caller
can always tell the difference between "this worked" and "this is impossible
here". The contract is asserted at compile time in `api_contract.go`, so a
method cannot be added to one platform without the others.

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
| Hotplug notifications | no | no | no |

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
cgo-based backend to choose between anymore (see #14, and the history of
PRs #15-#22 for how it got there).

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

### No hotplug

No backend implements hotplug notifications. Poll `DeviceList` if you need to
detect changes.

## Limitations

- Requires appropriate permissions for USB device access
- No hotplug support (can be implemented with platform-specific monitoring)
- Async transfers on macOS require CFRunLoop integration
- The Windows backend is newer than the Linux and macOS ones; isochronous and
  asynchronous transfers are not implemented there yet

## Resources

- [USB 2.0 Specification](https://www.usb.org/document-library/usb-20-specification)
- [Linux usbfs Documentation](https://www.kernel.org/doc/html/latest/driver-api/usb/index.html)
- [macOS IOKit USB Documentation](https://developer.apple.com/documentation/iokit)
- [libusb Documentation](https://libusb.info/)
