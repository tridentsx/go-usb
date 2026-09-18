# Command-line tools

Every program here is a real, runnable example of using
[github.com/tridentsx/go-usb](https://github.com/tridentsx/go-usb) — most
double as diagnostic/browsing tools in their own right, and a few are
verification suites written against real hardware rather than mocks. They
build and run on Linux, macOS and Windows unless a tool's own section below
says otherwise (a handful exercise Linux-only capabilities, like
`DetachKernelDriver`, and say so explicitly).

Build any of them with:

```bash
go build ./cmd/<name>
```

## lsusb

A `lsusb`-like device lister, deliberately styled after the real Linux
`lsusb` tool's output and flags (`-v`, `-t`, `-d`, `-s`, `-D`) so it is
usable as a drop-in replacement on platforms that do not have one.

```bash
go build ./cmd/lsusb
./lsusb                        # list every device
./lsusb -v                     # verbose: descriptors, interfaces, endpoints
./lsusb -t                     # tree view (bus/hub topology)
./lsusb -d 1234:5678           # filter to one VID:PID
./lsusb -s 1:6                 # filter to one [bus]:[devnum]
./lsusb -D <device-path>       # show one device by its platform-specific Path
./lsusb -V                     # version
```

The tree view (`-t`) degrades to a flat listing on macOS, where `Device.Port`/
`ParentHubAddr` are not populated yet — see the main README's Platform
Support Matrix.

## listconfigs

Lists every device's configuration descriptors in a hierarchical format:
Config → Interface → Alt Setting → Endpoint, including class-specific
descriptors carried in each descriptor's `Extra` field and SuperSpeed
companion descriptors when present.

```bash
go build ./cmd/listconfigs
./listconfigs                        # every device
./listconfigs -vid 0x1234 -pid 0x5678
./listconfigs -v                     # verbose: endpoints and extra descriptors
```

## capabilities

Reads USB 3.0+ capability descriptors: the BOS (Binary Object Store)
descriptor, USB 2.0 Extension capabilities (Link Power Management support,
BESL parameters), SuperSpeed USB capabilities (supported speeds, exit
latencies), and SuperSpeed endpoint companion descriptors (burst size,
stream support).

```bash
go build ./cmd/capabilities
./capabilities
./capabilities -vid 0x1234 -pid 0x5678
```

Demonstrates [`DeviceHandle.ReadBOSDescriptor`](../compat_windows.go),
[`DeviceHandle.USB20ExtensionDescriptor`](../compat_windows.go),
[`DeviceHandle.SSUSBDeviceCapabilityDescriptor`](../compat_windows.go) and
[`DeviceHandle.SSEndpointCompanionDescriptor`](../device_windows.go).
A device with no USB 3.0+ capabilities reports so rather than erroring —
these descriptors are genuinely optional.

## browse-msc

Browses a USB Mass Storage (SCSI-over-bulk) device's descriptors: its Mass
Storage interface, bulk endpoints, and (with `-list`) every mass storage
device currently attached. Defaults to a SanDisk Ultra's VID:PID as a
placeholder, not a requirement.

```bash
go build ./cmd/browse-msc
./browse-msc -list                     # list every mass storage device
./browse-msc -vid 0781 -pid 5581        # browse a specific device
```

## browse-uvc

Browses a USB Video Class (UVC) device's descriptors: video control and
streaming interfaces, format/frame descriptors, and endpoints. `-auto`
finds any UVC webcam without needing its VID:PID.

```bash
go build ./cmd/browse-uvc
./browse-uvc -auto                      # auto-detect any UVC webcam
./browse-uvc -list                      # list every UVC device
./browse-uvc -vid 046d -pid 08e5        # browse a specific device (Logitech C920)
```

## otg-demo

Finds USB On-The-Go (OTG) capable devices, reads their OTG descriptor, and
demonstrates the HNP (Host Negotiation Protocol) feature selectors
(`SetFeature`/`ClearFeature` with `USB_DEVICE_B_HNP_ENABLE` etc.). OTG is a
USB 2.0-era feature; most modern devices will not report as OTG-capable, in
which case this reports that plainly rather than treating it as an error.

```bash
go build ./cmd/otg-demo
./otg-demo
```

## test-driver-management

Exercises the claim/detach/reattach lifecycle end to end against a real
device: `ClaimInterface`, `DetachKernelDriver`, re-claiming after detach,
`ReleaseInterface`, `AttachKernelDriver`. **Linux-only** in practice —
`DetachKernelDriver`/`AttachKernelDriver` report `ErrNotSupported` on macOS
and Windows, which this tool will show plainly rather than crash on.

```bash
go build ./cmd/test-driver-management
sudo ./test-driver-management   # needs root on Linux to detach a driver
```

## test-extended-features

A broader verification pass per device: `Status`, `Speed`, `Capabilities`,
BOS and Device Qualifier descriptors, zero-length packet handling
(`BulkTransfer` rejects an empty buffer; `BulkTransferWithOptions` with
`allowZeroLength` does not), and basic error-recovery behavior.

```bash
go build ./cmd/test-extended-features
./test-extended-features
```

## test-ss-descriptors

Focused SuperSpeed (USB 3.0+) descriptor verification: device speed,
BOS/USB2Extension/SuperSpeedUSB capabilities, Device Qualifier, and
per-endpoint SuperSpeed companion descriptors walked from a real
configuration descriptor.

```bash
go build ./cmd/test-ss-descriptors
./test-ss-descriptors
```

## verify-transfers

The broadest verification suite here: device descriptor sanity (including
vendor/product name lookup), control transfers, and interface
claim/detach/release. Written to report each check's pass/fail rather than
stopping at the first failure, so one bad device does not hide results for
the rest.

```bash
go build ./cmd/verify-transfers
./verify-transfers
```

## test

A minimal bulk read/write smoke test against whatever real device looks
most like a plain data device (preferring non-hub devices). Useful as a
quick "does this board respond at all" check before reaching for one of
the more targeted tools above.

```bash
go run ./cmd/test
```

## Requirements

- **Permissions**: reading descriptors needs no special privilege on any
  platform; opening a device and claiming an interface needs it be
  accessible per the main README's Permissions section (a udev rule on
  Linux, no special setup on macOS, WinUSB bound via Zadig or an INF on
  Windows).
- **Root/administrator**, specifically: `test-driver-management` and any
  tool calling `DetachKernelDriver` on Linux.
- **USB 3.0+ hardware and a SuperSpeed port**: `capabilities` and
  `test-ss-descriptors` report "not present" rather than erroring when
  neither is available, but obviously show nothing interesting either.
