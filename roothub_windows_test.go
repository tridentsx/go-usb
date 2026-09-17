//go:build windows

package usb

import "testing"

// TestParseVidPidFromHardwareID checks that the parser correctly handles the
// &VIDxxxx&PIDyyyy format used in USB root hub hardware IDs, which differs
// from the vid_xxxx&pid_yyyy format in device paths.
func TestParseVidPidFromHardwareID(t *testing.T) {
	tests := []struct {
		hwID    string
		wantVID uint16
		wantPID uint16
	}{
		// Full hardware ID as Windows reports it for an Intel root hub.
		{"USB\\ROOT_HUB30&VID8086&PID9A13&REV0000", 0x8086, 0x9A13},
		// Minimal form (no revision suffix).
		{"USB\\ROOT_HUB30&VID8086&PID9A13", 0x8086, 0x9A13},
		// USB 2.0 root hub.
		{"USB\\ROOT_HUB20&VID1234&PIDEF01", 0x1234, 0xEF01},
		// Legacy USB 1.1 root hub with no version suffix.
		{"USB\\ROOT_HUB&VID0000&PID0001", 0x0000, 0x0001},
		// No VID/PID in string.
		{"USB\\ROOT_HUB30", 0, 0},
		// Empty string.
		{"", 0, 0},
	}

	for _, tt := range tests {
		vid, pid := parseVidPidFromHardwareID(tt.hwID)
		if vid != tt.wantVID || pid != tt.wantPID {
			t.Errorf("parseVidPidFromHardwareID(%q) = (%04x, %04x), want (%04x, %04x)",
				tt.hwID, vid, pid, tt.wantVID, tt.wantPID)
		}
	}
}
