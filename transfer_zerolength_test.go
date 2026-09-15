package usb

import (
	"testing"
	"time"
)

// TestZeroLengthTransferRejection pins down the zero-length packet contract that
// every backend must share: an empty buffer is rejected with ErrInvalidParameter
// unless the caller explicitly opts in via BulkTransferWithOptions.
//
// These cases deliberately stop before any I/O is attempted, so they need no
// device: each backend validates the buffer length before touching a file
// descriptor, an IOKit interface, or a WinUSB pipe.
func TestZeroLengthTransferRejection(t *testing.T) {
	tests := []struct {
		name string
		call func(h *DeviceHandle) (int, error)
		want error
	}{
		{
			name: "BulkTransfer rejects empty buffer",
			call: func(h *DeviceHandle) (int, error) {
				return h.BulkTransfer(0x02, nil, time.Second)
			},
			want: ErrInvalidParameter,
		},
		{
			name: "BulkTransferWithOptions rejects empty buffer when not allowed",
			call: func(h *DeviceHandle) (int, error) {
				return h.BulkTransferWithOptions(0x02, nil, time.Second, false)
			},
			want: ErrInvalidParameter,
		},
		{
			name: "BulkTransferWithOptions rejects empty non-nil buffer when not allowed",
			call: func(h *DeviceHandle) (int, error) {
				return h.BulkTransferWithOptions(0x02, []byte{}, time.Second, false)
			},
			want: ErrInvalidParameter,
		},
		{
			name: "InterruptTransfer rejects empty buffer",
			call: func(h *DeviceHandle) (int, error) {
				return h.InterruptTransfer(0x81, nil, time.Second)
			},
			want: ErrInvalidParameter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := tt.call(&DeviceHandle{})
			if err != tt.want {
				t.Errorf("got error %v, want %v", err, tt.want)
			}
			if n != 0 {
				t.Errorf("got %d bytes transferred, want 0", n)
			}
		})
	}
}
