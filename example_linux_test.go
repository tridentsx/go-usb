package usb_test

// Linux-only capabilities: raw access to a HID-class interface (more
// capable than macOS/Windows' report-level HID transport, since it carries
// vendor control requests too, but needs root or a udev rule), and USB 3.0
// bulk streams, which neither macOS nor Windows expose through the API
// this package's backends use. See the package doc comment and the
// README's "Bulk streams are Linux-only for now" section.

import (
	"log"
	"time"

	usb "github.com/tridentsx/go-usb"
)

// ExampleDeviceHandle_DetachKernelDriver unbinds the usbhid kernel driver
// from an interface so it can be claimed and read with raw transfers
// instead of going through a report-level HID API.
func ExampleDeviceHandle_DetachKernelDriver() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	const iface = 0
	active, err := handle.KernelDriverActive(iface)
	if err != nil {
		log.Fatal(err)
	}
	if active {
		if err := handle.DetachKernelDriver(iface); err != nil {
			log.Fatal(err) // needs root, or a udev rule granting this
		}
		defer handle.AttachKernelDriver(iface)
	}

	if err := handle.ClaimInterface(iface); err != nil {
		log.Fatal(err)
	}
	defer handle.ReleaseInterface(iface)

	buf := make([]byte, 64)
	if _, err := handle.InterruptTransfer(0x81, buf, time.Second); err != nil {
		log.Fatal(err)
	}
}

// ExampleDeviceHandle_AllocStreams allocates USB 3.0 bulk streams on a set
// of endpoints, letting a single bulk endpoint carry several independently
// queued transfers -- primarily useful for USB Attached SCSI (UASP)
// mass-storage devices; see the README for why this is unlikely to matter
// for typical test/measurement equipment.
func ExampleDeviceHandle_AllocStreams() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	endpoints := []uint8{0x01, 0x82}
	const numStreams = 4

	if err := handle.AllocStreams(numStreams, endpoints); err != nil {
		log.Fatal(err)
	}
	defer handle.FreeStreams(endpoints)
}
