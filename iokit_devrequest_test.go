package usb

import (
	"testing"
	"unsafe"
)

// TestIOUSBDevRequestLayout pins the layout IOKit's DeviceRequest expects.
//
// A wrong offset here does not crash: it sends a control transfer with the wrong
// request type, value or length, which either fails against the device or, worse,
// succeeds as a different request than intended.
func TestIOUSBDevRequestLayout(t *testing.T) {
	var r ioUSBDevRequest

	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"bmRequestType", unsafe.Offsetof(r.bmRequestType), 0},
		{"bRequest", unsafe.Offsetof(r.bRequest), 1},
		{"wValue", unsafe.Offsetof(r.wValue), 2},
		{"wIndex", unsafe.Offsetof(r.wIndex), 4},
		{"wLength", unsafe.Offsetof(r.wLength), 6},
		{"pData", unsafe.Offsetof(r.pData), 8},
		{"wLenDone", unsafe.Offsetof(r.wLenDone), 16},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("offset of %s = %d, want %d", tt.name, tt.got, tt.want)
		}
	}

	if size := unsafe.Sizeof(r); size != 24 {
		t.Errorf("sizeof(ioUSBDevRequest) = %d, want 24", size)
	}
}
