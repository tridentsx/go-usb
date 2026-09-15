package usb

import (
	"os"
	"testing"
)

// TestDeviceListWithoutSysfsUSB checks that a machine with no USB support in
// sysfs reports an empty device list rather than an error.
//
// Containers, minimal kernels and WSL2 all lack /sys/bus/usb, and the macOS
// backend reports "no devices" in the equivalent situation, so the Linux
// backend must not treat it as a failure.
func TestDeviceListWithoutSysfsUSB(t *testing.T) {
	if _, err := os.Stat("/sys/bus/usb/devices"); err == nil {
		t.Skip("this machine has a sysfs USB tree; nothing to assert")
	}

	devices, err := DeviceList()
	if err != nil {
		t.Fatalf("DeviceList returned %v, want nil error when sysfs USB is absent", err)
	}
	if devices == nil {
		t.Fatal("DeviceList returned a nil slice, want an empty one")
	}
	if len(devices) != 0 {
		t.Fatalf("DeviceList returned %d devices, want 0", len(devices))
	}
}
