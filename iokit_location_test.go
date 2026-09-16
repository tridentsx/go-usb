package usb

import (
	"reflect"
	"testing"
)

func TestBusFromLocationID(t *testing.T) {
	tests := []struct {
		locationID uint32
		want       uint8
	}{
		{0x14200000, 0x14},
		{0xfa100000, 0xfa},
		{0x00000000, 0x00},
		{0x01000000, 0x01},
	}

	for _, tt := range tests {
		if got := busFromLocationID(tt.locationID); got != tt.want {
			t.Errorf("busFromLocationID(%#08x) = %#02x, want %#02x",
				tt.locationID, got, tt.want)
		}
	}
}

func TestPortChainFromLocationID(t *testing.T) {
	tests := []struct {
		name       string
		locationID uint32
		want       []uint8
	}{
		{"root port only", 0x14100000, []uint8{1}},
		{"one hub deep", 0x14210000, []uint8{2, 1}},
		{"three deep", 0x14321000, []uint8{3, 2, 1}},
		{"controller with no ports", 0x14000000, nil},
		{"zero", 0x00000000, nil},
		{"full depth", 0x14123456, []uint8{1, 2, 3, 4, 5, 6}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := portChainFromLocationID(tt.locationID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("portChainFromLocationID(%#08x) = %v, want %v",
					tt.locationID, got, tt.want)
			}
		})
	}
}

// TestIOKitDevicePath pins the path format, which must match the cgo backend
// exactly so that both are accepted by IsValidDevicePath and callers cannot tell
// which backend produced a device.
func TestIOKitDevicePath(t *testing.T) {
	tests := []struct {
		locationID uint32
		want       string
	}{
		{0x14200000, "iokit:14200000"},
		{0x00000001, "iokit:00000001"},
		{0xfa130000, "iokit:fa130000"},
	}

	for _, tt := range tests {
		if got := iokitDevicePath(tt.locationID); got != tt.want {
			t.Errorf("iokitDevicePath(%#08x) = %q, want %q", tt.locationID, got, tt.want)
		}
	}
}
