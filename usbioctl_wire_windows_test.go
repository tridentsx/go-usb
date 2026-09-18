package usb

import (
	"bytes"
	"testing"
)

// TestUSBCtlCode checks the computed control codes against the values the
// Windows headers define, so a mistake in the CTL_CODE arithmetic cannot slip
// through unnoticed.
func TestUSBCtlCode(t *testing.T) {
	tests := []struct {
		name     string
		function uint32
		want     uint32
	}{
		{"IOCTL_USB_GET_NODE_INFORMATION", usbGetNodeInformation, 0x220408},
		{"IOCTL_USB_GET_NODE_CONNECTION_INFORMATION", usbGetNodeConnectionInformation, 0x22040c},
		{"IOCTL_USB_GET_DESCRIPTOR_FROM_NODE_CONNECTION", usbGetDescriptorFromNodeConnection, 0x220410},
		{"IOCTL_USB_GET_NODE_CONNECTION_NAME", usbGetNodeConnectionName, 0x220414},
		{"IOCTL_USB_GET_NODE_CONNECTION_DRIVERKEY_NAME", usbGetNodeConnectionDriverKeyName, 0x220420},
		{"IOCTL_USB_GET_NODE_CONNECTION_INFORMATION_EX", usbGetNodeConnectionInformationEx, 0x220448},
		{"IOCTL_USB_GET_NODE_CONNECTION_INFORMATION_EX_V2", usbGetNodeConnectionInformationExV2, 0x22045c},
	}

	for _, tt := range tests {
		if got := usbCtlCode(tt.function); got != tt.want {
			t.Errorf("%s = %#x, want %#x", tt.name, got, tt.want)
		}
	}
}

// TestParseNodeConnection decodes a buffer captured from a real hub. The reply
// described a Realtek USB 2.1 hub on port 1 at device address 5, and the
// driver returned 46 bytes: 35 of fixed fields plus one 11-byte pipe entry.
//
// This is the regression test for the packing: with natural Go alignment,
// DeviceAddress reads from offset 26 and ConnectionStatus from 32, which makes
// a connected device look absent.
func TestParseNodeConnection(t *testing.T) {
	captured := []byte{
		0x01, 0x00, 0x00, 0x00, // ConnectionIndex = 1
		0x12, 0x01, 0x10, 0x02, 0x09, 0x00, 0x02, 0x40, // device descriptor
		0xda, 0x0b, 0x09, 0x54, 0x58, 0x01, 0x01, 0x02,
		0x00, 0x01,
		0x01,       // CurrentConfigurationValue
		0x02,       // Speed = high
		0x01,       // DeviceIsHub = true
		0x05, 0x00, // DeviceAddress = 5 (offset 25)
		0x01, 0x00, 0x00, 0x00, // NumberOfOpenPipes = 1
		0x01, 0x00, 0x00, 0x00, // ConnectionStatus = DeviceConnected (offset 31)
		0x07, 0x05, 0x81, 0x03, 0x01, 0x00, 0x0c, 0x00, 0x00, 0x00, 0x00, // pipe
	}

	if len(captured) != 46 {
		t.Fatalf("captured buffer is %d bytes, expected the 46 the driver returned", len(captured))
	}

	nc, ok := parseNodeConnection(captured)
	if !ok {
		t.Fatal("parseNodeConnection reported the buffer as too short")
	}

	if !nc.Connected {
		t.Error("Connected = false, want true; check the ConnectionStatus offset")
	}
	if !nc.DeviceIsHub {
		t.Error("DeviceIsHub = false, want true")
	}
	if nc.DeviceAddress != 5 {
		t.Errorf("DeviceAddress = %d, want 5; check for padding before the field", nc.DeviceAddress)
	}
	if nc.Speed != usbHighSpeed {
		t.Errorf("Speed = %d, want %d", nc.Speed, usbHighSpeed)
	}
	if nc.ConfigValue != 1 {
		t.Errorf("ConfigValue = %d, want 1", nc.ConfigValue)
	}
	if nc.Descriptor.VendorID != 0x0bda || nc.Descriptor.ProductID != 0x5409 {
		t.Errorf("descriptor = %04x:%04x, want 0bda:5409",
			nc.Descriptor.VendorID, nc.Descriptor.ProductID)
	}
	if nc.Descriptor.USBVersion != 0x0210 {
		t.Errorf("USBVersion = %#04x, want 0x0210", nc.Descriptor.USBVersion)
	}
	if nc.Descriptor.DeviceClass != 0x09 {
		t.Errorf("DeviceClass = %#02x, want 0x09 (hub)", nc.Descriptor.DeviceClass)
	}
}

// TestParseNodeConnectionEmptyPort uses a reply from a port with nothing
// attached. The driver returns exactly nodeConnectionInfoSize bytes, which is
// how that constant was established.
func TestParseNodeConnectionEmptyPort(t *testing.T) {
	buf := make([]byte, nodeConnectionInfoSize)
	buf[0] = 2 // ConnectionIndex

	nc, ok := parseNodeConnection(buf)
	if !ok {
		t.Fatal("parseNodeConnection reported the buffer as too short")
	}
	if nc.Connected {
		t.Error("Connected = true for an empty port, want false")
	}
}

func TestParseNodeConnectionTooShort(t *testing.T) {
	if _, ok := parseNodeConnection(make([]byte, nodeConnectionInfoSize-1)); ok {
		t.Error("parseNodeConnection accepted a short buffer")
	}
}

func TestEncodeNodeConnectionRequest(t *testing.T) {
	buf := encodeNodeConnectionRequest(7, 4)

	want := nodeConnectionInfoSize + 4*11
	if len(buf) != want {
		t.Errorf("buffer is %d bytes, want %d", len(buf), want)
	}
	if buf[0] != 7 {
		t.Errorf("ConnectionIndex = %d, want 7", buf[0])
	}
}

func TestEncodeDescriptorRequest(t *testing.T) {
	buf := encodeDescriptorRequest(3, USB_DT_STRING, 2, 0x0409, 255)

	if len(buf) != descriptorRequestHeaderSize+255 {
		t.Fatalf("buffer is %d bytes, want %d", len(buf), descriptorRequestHeaderSize+255)
	}
	if buf[0] != 3 {
		t.Errorf("ConnectionIndex = %d, want 3", buf[0])
	}

	// The setup packet follows the connection index.
	wantSetup := []byte{0x80, 0x06, 0x02, 0x03, 0x09, 0x04, 0xff, 0x00}
	if !bytes.Equal(buf[4:12], wantSetup) {
		t.Errorf("setup packet = % x, want % x", buf[4:12], wantSetup)
	}
}

func TestParseDescriptorReply(t *testing.T) {
	buf := make([]byte, descriptorRequestHeaderSize+8)
	copy(buf[descriptorRequestHeaderSize:], []byte{0x08, 0x03, 'H', 0, 'i', 0})

	got := parseDescriptorReply(buf, descriptorRequestHeaderSize+6)
	if want := []byte{0x08, 0x03, 'H', 0, 'i', 0}; !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}

	if parseDescriptorReply(buf, descriptorRequestHeaderSize) != nil {
		t.Error("a header-only reply should yield no descriptor")
	}
	if parseDescriptorReply(buf, uint32(len(buf)+1)) != nil {
		t.Error("a reply longer than the buffer should be rejected")
	}
}

func TestSpeedToSpeed(t *testing.T) {
	tests := map[uint8]Speed{
		usbLowSpeed:   SpeedLow,
		usbFullSpeed:  SpeedFull,
		usbHighSpeed:  SpeedHigh,
		usbSuperSpeed: SpeedSuper,
		0xff:          SpeedUnknown,
	}
	for in, want := range tests {
		if got := speedToSpeed(in); got != want {
			t.Errorf("speedToSpeed(%d) = %v, want %v", in, got, want)
		}
	}
}
