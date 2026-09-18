package usb

import "testing"

// TestHIDFallbackAgainstRealHardware exercises openHIDInterface's fallback
// path against whatever is actually plugged in, rather than requiring a
// specific device the way the jig-gated tests do: it claims every interface
// on every enumerable device, and if any of them turns out to be HID-owned,
// exercises the report-level API against it for real.
//
// It skips rather than fails when nothing HID-owned is found (or nothing is
// plugged in at all), since that is a fact about the test machine, not a bug.
func TestHIDFallbackAgainstRealHardware(t *testing.T) {
	devices, err := DeviceList()
	if err != nil {
		t.Fatalf("DeviceList: %v", err)
	}

	var found bool
	for _, d := range devices {
		h, err := d.Open()
		if err != nil {
			continue
		}

		for iface := uint8(0); iface < 8; iface++ {
			if err := h.ClaimInterface(iface); err != nil {
				continue
			}
			if !h.IsHID() {
				h.ReleaseInterface(iface)
				continue
			}

			found = true
			t.Logf("HID interface found: %04x:%04x iface %d", d.Descriptor.VendorID, d.Descriptor.ProductID, iface)

			in, out, feat, err := h.HIDReportLengths(0)
			if err != nil {
				t.Errorf("HIDReportLengths: %v", err)
			} else {
				t.Logf("report lengths: input=%d output=%d feature=%d", in, out, feat)
			}

			if feat > 0 {
				buf := make([]byte, feat)
				if _, err := h.GetFeatureReport(0, buf); err != nil {
					t.Logf("GetFeatureReport(0): %v (not every device answers report ID 0)", err)
				}
			}

			h.ReleaseInterface(iface)
		}
		h.Close()
	}

	if !found {
		t.Skip("no accessible HID-owned interface found on this machine")
	}
}
