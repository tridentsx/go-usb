package usb

import (
	"errors"
	"testing"
)

// TestKernelDriverStubsReportNotSupported pins down that macOS reports
// ErrNotSupported for driver (de)tach instead of returning nil and letting the
// caller believe the operation succeeded.
//
// Linux implements these for real via USBDEVFS_DISCONNECT/CONNECT, which is a
// genuine capability difference; see the platform matrix in README.md.
func TestKernelDriverStubsReportNotSupported(t *testing.T) {
	h := &DeviceHandle{}

	if err := h.DetachKernelDriver(0); !errors.Is(err, ErrNotSupported) {
		t.Errorf("DetachKernelDriver returned %v, want ErrNotSupported", err)
	}

	if err := h.AttachKernelDriver(0); !errors.Is(err, ErrNotSupported) {
		t.Errorf("AttachKernelDriver returned %v, want ErrNotSupported", err)
	}
}
