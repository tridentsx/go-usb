//go:build darwin || windows

package usb_test

// HID report access is only implemented on macOS and Windows -- Linux
// reaches the same devices through DetachKernelDriver plus raw transfers
// instead (see example_linux_test.go), which is more capable but needs
// root or a udev rule. See the package doc comment and the README's
// "Accessing HID devices" section for why.

import (
	"fmt"
	"log"

	usb "github.com/tridentsx/go-usb"
)

// ExampleDeviceHandle_GetFeatureReport reads and writes a HID feature
// report, the way most HID-based test equipment exposes configuration and
// polled readings.
func ExampleDeviceHandle_GetFeatureReport() {
	handle, err := usb.OpenDevice(0x1234, 0x5678)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	if !handle.IsHID() {
		// This device is reached through WinUSB/IOUSBInterfaceInterface
		// instead; GetFeatureReport etc. only apply to a HID transport.
		return
	}

	const reportID = 0
	report := make([]byte, 8)
	if _, err := handle.GetFeatureReport(reportID, report); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("feature report: %x\n", report)

	report[0] = 0x01
	if err := handle.SetFeatureReport(reportID, report); err != nil {
		log.Fatal(err)
	}
}
