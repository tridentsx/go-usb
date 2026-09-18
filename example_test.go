package usb_test

// Runnable documentation for every portable capability this package
// offers, in the standard godoc Example form. None of these have an
// "Output:" comment, because their output depends on whatever real USB
// hardware happens to be attached to the machine running them -- per the
// testing package's own documented behavior, an Example without one is
// compiled (so it is checked against the real API on every push, see
// .github/workflows/go.yml) but never executed, which is exactly right
// here: there is no real device in a CI runner for these to talk to.

import (
	"fmt"
	"log"
	"time"

	usb "github.com/tridentsx/go-usb"
)

// Example lists every USB device the host can see. This is the same code
// shown in the README's "Quick Start".
func Example() {
	devices, err := usb.DeviceList()
	if err != nil {
		log.Fatal(err)
	}
	for _, dev := range devices {
		fmt.Printf("%04x:%04x\n", dev.Descriptor.VendorID, dev.Descriptor.ProductID)
	}
}

// ExampleDeviceList shows the two ways to open a device found by
// enumeration: by VID/PID directly, or from an already-enumerated [usb.Device].
func ExampleDeviceList() {
	devices, err := usb.DeviceList()
	if err != nil {
		log.Fatal(err)
	}
	if len(devices) == 0 {
		return
	}

	handle, err := devices[0].Open()
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()
}

// ExampleOpenDevice opens a device directly by vendor and product ID,
// without a separate DeviceList call.
func ExampleOpenDevice() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	desc := handle.Descriptor()
	manufacturer, err := handle.StringDescriptor(desc.ManufacturerIndex)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(manufacturer)
}

// ExampleDeviceHandle_ControlTransfer reads the device descriptor with a
// manual control transfer, the same GET_DESCRIPTOR request
// [usb.DeviceHandle.GetDeviceDescriptor] performs internally -- useful
// when a device needs a vendor- or class-specific control request instead.
func ExampleDeviceHandle_ControlTransfer() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	buf := make([]byte, 18)
	_, err = handle.ControlTransfer(
		0x80,          // bmRequestType: device-to-host, standard, device
		0x06,          // bRequest: GET_DESCRIPTOR
		0x0100,        // wValue: descriptor type 1 (DEVICE), index 0
		0x0000,        // wIndex
		buf,           // data buffer
		5*time.Second, // timeout
	)
	if err != nil {
		log.Fatal(err)
	}
}

// ExampleDeviceHandle_ClaimInterface claims an interface, performs a bulk
// transfer on one of its endpoints, and releases it -- the sequence every
// bulk, interrupt or isochronous transfer needs first.
func ExampleDeviceHandle_ClaimInterface() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	if err := handle.SetConfiguration(1); err != nil {
		log.Fatal(err)
	}

	const iface = 0
	if err := handle.ClaimInterface(iface); err != nil {
		log.Fatal(err)
	}
	defer handle.ReleaseInterface(iface)

	data := []byte("hello")
	if _, err := handle.BulkTransfer(0x02, data, 5*time.Second); err != nil {
		log.Fatal(err)
	}
}

// ExampleDeviceHandle_InterruptTransfer reads one report from an interrupt
// IN endpoint.
func ExampleDeviceHandle_InterruptTransfer() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	buf := make([]byte, 64)
	n, err := handle.InterruptTransfer(0x81, buf, 100*time.Millisecond)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("read %d bytes\n", n)
}

// ExampleDeviceHandle_NewBulkTransfer submits an asynchronous bulk read and
// waits for it, the real, working async API on every platform: Submit
// queues it, and WaitWithTimeout blocks until it completes or the timeout
// elapses, after which Status/ActualLength/Buffer describe what happened.
//
// NewInterruptTransfer and NewControlTransfer work identically for those
// transfer types; see [usb.AsyncTransfer].
//
// AsyncTransfer.SetCallback is deliberately not shown here: it only exists
// on macOS today (AsyncTransfer embeds *Transfer there, promoting it;
// Linux and Windows' AsyncTransfer are separate structs that do not), so
// Wait/WaitWithTimeout is the only portable way to learn a transfer
// finished.
func ExampleDeviceHandle_NewBulkTransfer() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	transfer, err := handle.NewBulkTransfer(0x81, 64)
	if err != nil {
		log.Fatal(err)
	}

	if err := transfer.Submit(); err != nil {
		log.Fatal(err)
	}
	if err := transfer.WaitWithTimeout(time.Second); err != nil {
		log.Fatal(err)
	}
	if transfer.Status() == usb.TransferCompleted {
		fmt.Printf("received %d bytes: %x\n", transfer.ActualLength(), transfer.Buffer())
	}
}

// ExampleDeviceHandle_NewIsochronousTransfer submits one isochronous
// transfer and reads each packet's own length and status back afterward --
// isochronous traffic has no synchronous form, since it is inherently a
// multi-packet streaming operation.
func ExampleDeviceHandle_NewIsochronousTransfer() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	const numPackets, packetSize = 8, 64
	transfer, err := handle.NewIsochronousTransfer(0x83, numPackets, packetSize)
	if err != nil {
		log.Fatal(err)
	}

	if err := transfer.Submit(); err != nil {
		log.Fatal(err)
	}
	if err := transfer.Wait(); err != nil {
		log.Fatal(err)
	}

	for i, pkt := range transfer.Packets() {
		fmt.Printf("packet %d: %d bytes, status %d\n", i, pkt.ActualLength, pkt.Status)
	}
}

// ExampleRegisterHotplugCallback watches for a specific device arriving or
// leaving. vendorID and productID of 0 would match every device instead.
func ExampleRegisterHotplugCallback() {
	handle, err := usb.RegisterHotplugCallback(0x1234, 0x5678, func(event usb.HotplugEvent, dev *usb.Device) {
		fmt.Printf("%s: %04x:%04x\n", event, dev.Descriptor.VendorID, dev.Descriptor.ProductID)
	})
	if err != nil {
		log.Fatal(err) // ErrNotSupported on a backend that cannot watch yet
	}
	defer handle.Deregister()

	time.Sleep(time.Minute) // keep watching
}

// ExampleDeviceHandle_GetConfigDescriptor reads and walks a configuration
// descriptor's interfaces and endpoints.
func ExampleDeviceHandle_GetConfigDescriptor() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	config, err := handle.GetConfigDescriptor(0)
	if err != nil {
		log.Fatal(err)
	}

	for _, iface := range config.Interfaces {
		for _, alt := range iface.AltSettings {
			fmt.Printf("interface %d alt %d: class %#x, %d endpoint(s)\n",
				alt.InterfaceNumber, alt.AlternateSetting, alt.InterfaceClass, len(alt.Endpoints))
		}
	}
}
