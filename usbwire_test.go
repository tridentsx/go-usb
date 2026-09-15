package usb

import (
	"bytes"
	"testing"
)

// TestEncodeSetupPacket pins the setup packet byte layout against USB 2.0
// Table 9-2. Windows passes this structure to WinUsb_ControlTransfer by value,
// so a wrong layout silently corrupts bmRequestType and bRequest rather than
// failing loudly.
func TestEncodeSetupPacket(t *testing.T) {
	tests := []struct {
		name        string
		requestType uint8
		request     uint8
		value       uint16
		index       uint16
		length      uint16
		want        []byte
	}{
		{
			name:        "GET_DESCRIPTOR device",
			requestType: 0x80,
			request:     USB_REQ_GET_DESCRIPTOR,
			value:       descriptorRequestValue(USB_DT_DEVICE, 0),
			index:       0,
			length:      18,
			want:        []byte{0x80, 0x06, 0x00, 0x01, 0x00, 0x00, 0x12, 0x00},
		},
		{
			name:        "GET_DESCRIPTOR string index 2, English (US)",
			requestType: 0x80,
			request:     USB_REQ_GET_DESCRIPTOR,
			value:       descriptorRequestValue(USB_DT_STRING, 2),
			index:       0x0409,
			length:      255,
			want:        []byte{0x80, 0x06, 0x02, 0x03, 0x09, 0x04, 0xff, 0x00},
		},
		{
			name:        "SET_CONFIGURATION",
			requestType: 0x00,
			request:     USB_REQ_SET_CONFIGURATION,
			value:       1,
			index:       0,
			length:      0,
			want:        []byte{0x00, 0x09, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name:        "all 16-bit fields little-endian",
			requestType: 0xc1,
			request:     0x42,
			value:       0x1234,
			index:       0xabcd,
			length:      0x0100,
			want:        []byte{0xc1, 0x42, 0x34, 0x12, 0xcd, 0xab, 0x00, 0x01},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeSetupPacket(tt.requestType, tt.request, tt.value, tt.index, tt.length)
			if !bytes.Equal(got[:], tt.want) {
				t.Errorf("encodeSetupPacket() = % x, want % x", got, tt.want)
			}
		})
	}
}

func TestDescriptorRequestValue(t *testing.T) {
	tests := []struct {
		descType  uint8
		descIndex uint8
		want      uint16
	}{
		{USB_DT_DEVICE, 0, 0x0100},
		{USB_DT_CONFIG, 0, 0x0200},
		{USB_DT_CONFIG, 3, 0x0203},
		{USB_DT_STRING, 2, 0x0302},
		{USB_DT_BOS, 0, 0x0f00},
	}

	for _, tt := range tests {
		if got := descriptorRequestValue(tt.descType, tt.descIndex); got != tt.want {
			t.Errorf("descriptorRequestValue(%#x, %d) = %#04x, want %#04x",
				tt.descType, tt.descIndex, got, tt.want)
		}
	}
}

func TestDecodeStringDescriptor(t *testing.T) {
	tests := []struct {
		name string
		desc []byte
		want string
	}{
		{
			name: "ascii",
			desc: []byte{0x0a, 0x03, 'H', 0, 'u', 0, 'b', 0, '!', 0},
			want: "Hub!",
		},
		{
			name: "declared length shorter than buffer",
			desc: []byte{0x06, 0x03, 'H', 0, 'i', 0, 'X', 0, 'Y', 0},
			want: "Hi",
		},
		{
			name: "truncated read is tolerated",
			desc: []byte{0x0a, 0x03, 'H', 0, 'i', 0},
			want: "Hi",
		},
		{
			name: "non-ascii",
			desc: []byte{0x08, 0x03, 0xe9, 0x00, 0x6c, 0x00, 0xe8, 0x00},
			want: "élè",
		},
		{
			name: "stops at embedded NUL",
			desc: []byte{0x0a, 0x03, 'A', 0, 0, 0, 'B', 0, 'C', 0},
			want: "A",
		},
		{
			name: "empty",
			desc: []byte{0x02, 0x03},
			want: "",
		},
		{
			name: "too short to be a descriptor",
			desc: []byte{0x01},
			want: "",
		},
		{
			name: "nil",
			desc: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeStringDescriptor(tt.desc); got != tt.want {
				t.Errorf("decodeStringDescriptor(% x) = %q, want %q", tt.desc, got, tt.want)
			}
		})
	}
}
