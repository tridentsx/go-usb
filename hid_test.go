package usb

import (
	"bytes"
	"testing"
)

// TestHIDCollectionUsable pins the selection policy. The usage pages here were
// observed on a real machine: the excluded ones are exactly those Windows also
// refuses to open for reading, and the allowed ones are the vendor-defined and
// control pages that test equipment uses.
func TestHIDCollectionUsable(t *testing.T) {
	tests := []struct {
		name      string
		usagePage uint16
		usage     uint16
		want      bool
	}{
		{"mouse", 0x01, 0x02, false},
		{"pointer", 0x01, 0x01, false},
		{"keyboard", 0x01, 0x06, false},
		{"keypad", 0x01, 0x07, false},
		{"digitizer touchpad", 0x0d, 0x05, false},
		{"digitizer pen", 0x0d, 0x02, false},

		{"system control on generic desktop", 0x01, 0x80, true},
		{"consumer control, used by monitors", 0x0c, 0x01, true},
		{"vendor defined ff99", 0xff99, 0x01, true},
		{"vendor defined ffda", 0xffda, 0xda, true},
		{"vendor defined ff00", 0xff00, 0x01, true},
		{"vendor defined ff83", 0xff83, 0x80, true},
		{"telephony", 0x0b, 0x05, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hidCollectionUsable(tt.usagePage, tt.usage); got != tt.want {
				t.Errorf("hidCollectionUsable(%#04x, %#04x) = %v, want %v",
					tt.usagePage, tt.usage, got, tt.want)
			}
		})
	}
}

// TestHIDPathInterfaceNumber uses paths captured from a real machine, since the
// endpoint-to-collection mapping depends entirely on reading them correctly.
func TestHIDPathInterfaceNumber(t *testing.T) {
	tests := []struct {
		path string
		want uint8
	}{
		{`\\?\HID#VID_413C&PID_4503&MI_02&Col02#8&2a20c0c&0&0001#{4d1e55b2-f16f-11cf-88cb-001111000030}`, 2},
		{`\\?\HID#VID_413C&PID_4503&MI_00&Col01#7&1a2b3c&0&0000#{4d1e55b2-f16f-11cf-88cb-001111000030}`, 0},
		{`\\?\HID#VID_0BDA&PID_1100&MI_05&Col03#8&abcdef&0&0005#{4d1e55b2-f16f-11cf-88cb-001111000030}`, 5},
		{`\\?\HID#VID_0408&PID_5474&MI_10&Col01#7&x&0&0010#{4d1e55b2-f16f-11cf-88cb-001111000030}`, 10},
		// No MI_ element: the device's only function is HID, interface 0.
		{`\\?\HID#VID_04F3&PID_0C9F#6&33560e95&0&4#{4d1e55b2-f16f-11cf-88cb-001111000030}`, 0},
		// Windows is inconsistent about case.
		{`\\?\hid#vid_413c&pid_4503&mi_03&col01#8&x&0&0001#{4d1e55b2-f16f-11cf-88cb-001111000030}`, 3},
	}

	for _, tt := range tests {
		if got := hidPathInterfaceNumber(tt.path); got != tt.want {
			t.Errorf("hidPathInterfaceNumber(%.60s...) = %d, want %d", tt.path, got, tt.want)
		}
	}
}

func TestHIDPathCollection(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{`\\?\HID#VID_413C&PID_4503&MI_02&Col02#8&x&0&0001#{guid}`, 2},
		{`\\?\HID#VID_413C&PID_4503&MI_02&Col01#8&x&0&0001#{guid}`, 1},
		{`\\?\HID#VID_413C&PID_4503&MI_02&Col11#8&x&0&0001#{guid}`, 11},
		// No collection element means a single collection.
		{`\\?\HID#VID_04F3&PID_0C9F#6&x&0&4#{guid}`, 1},
	}

	for _, tt := range tests {
		if got := hidPathCollection(tt.path); got != tt.want {
			t.Errorf("hidPathCollection(%.50s...) = %d, want %d", tt.path, got, tt.want)
		}
	}
}

func TestHIDReportBuffer(t *testing.T) {
	t.Run("pads to the report length", func(t *testing.T) {
		got, err := hidReportBuffer([]byte{0x00, 0x41, 0x42}, 8)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []byte{0x00, 0x41, 0x42, 0, 0, 0, 0, 0}
		if !bytes.Equal(got, want) {
			t.Errorf("got % x, want % x", got, want)
		}
	})

	t.Run("exact length is unchanged", func(t *testing.T) {
		got, err := hidReportBuffer([]byte{1, 2, 3, 4}, 4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
			t.Errorf("got % x, want 01 02 03 04", got)
		}
	})

	t.Run("oversized payload is rejected, not truncated", func(t *testing.T) {
		if _, err := hidReportBuffer(make([]byte, 9), 8); err != ErrOverflow {
			t.Errorf("got %v, want ErrOverflow", err)
		}
	})

	t.Run("device with no output reports", func(t *testing.T) {
		if _, err := hidReportBuffer([]byte{1}, 0); err != ErrNotSupported {
			t.Errorf("got %v, want ErrNotSupported", err)
		}
	})
}

func TestHIDTrimInputReport(t *testing.T) {
	report := []byte{0x00, 0xde, 0xad, 0xbe, 0xef}

	t.Run("fits", func(t *testing.T) {
		into := make([]byte, 8)
		n := hidTrimInputReport(report, into)
		if n != 5 {
			t.Errorf("n = %d, want 5", n)
		}
		if !bytes.Equal(into[:n], report) {
			t.Errorf("got % x, want % x", into[:n], report)
		}
	})

	t.Run("caller buffer is smaller", func(t *testing.T) {
		into := make([]byte, 3)
		n := hidTrimInputReport(report, into)
		if n != 3 {
			t.Errorf("n = %d, want 3", n)
		}
		if !bytes.Equal(into, report[:3]) {
			t.Errorf("got % x, want % x", into, report[:3])
		}
	})
}
