package usb

import (
	"testing"
	"time"
)

// TestHotplugDeregisterDoesNotDeadlock is a real regression test for a
// deadlock found on real hardware: Deregister used to close the netlink
// socket and wait for pump to exit, but closing a raw socket fd from
// another goroutine does not reliably unblock a Recvfrom already blocked
// on it in the Linux kernel, so pump could sit in that Recvfrom forever
// and Deregister would never return. This reproduces without any real USB
// device or hotplug event at all -- the deadlock was in the shutdown path
// itself, triggered by Deregister running before any uevent ever arrives,
// which is exactly what a register-then-immediately-deregister call does.
func TestHotplugDeregisterDoesNotDeadlock(t *testing.T) {
	handle, err := RegisterHotplugCallback(0, 0, func(HotplugEvent, *Device) {})
	if err != nil {
		t.Fatalf("RegisterHotplugCallback: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- handle.Deregister() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Deregister: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deregister did not return within 5s -- deadlocked (see this test's own doc comment for the real bug it guards against)")
	}
}

// TestParseUevent uses a payload shaped like a real kernel USB device add
// uevent (see kobject_uevent_env() in the kernel source for the real field
// set): a NUL-terminated "ACTION@DEVPATH" header line, redundant with its
// own ACTION=/DEVPATH= fields and skipped rather than parsed twice, followed
// by one NUL-terminated KEY=VALUE line per environment variable.
func TestParseUevent(t *testing.T) {
	raw := "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
		"ACTION=add\x00" +
		"DEVPATH=/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
		"SUBSYSTEM=usb\x00" +
		"DEVTYPE=usb_device\x00" +
		"BUSNUM=001\x00" +
		"DEVNUM=005\x00" +
		"PRODUCT=925/3881/100\x00" +
		"SEQNUM=12345\x00"

	fields := parseUevent([]byte(raw))

	want := map[string]string{
		"ACTION":    "add",
		"DEVPATH":   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
		"SUBSYSTEM": "usb",
		"DEVTYPE":   "usb_device",
		"BUSNUM":    "001",
		"DEVNUM":    "005",
		"PRODUCT":   "925/3881/100",
		"SEQNUM":    "12345",
	}
	for k, v := range want {
		if got := fields[k]; got != v {
			t.Errorf("fields[%q] = %q, want %q", k, got, v)
		}
	}
	// The header line has no '=' in "add@/devices/..." and must not produce
	// a spurious entry.
	if len(fields) != len(want) {
		t.Errorf("got %d fields, want %d: %v", len(fields), len(want), fields)
	}
}

// TestParseUeventInterface uses a payload shaped like a per-interface
// uevent from a composite device, which handleUevent must not treat as a
// whole-device arrival.
func TestParseUeventInterface(t *testing.T) {
	raw := "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1:1.0\x00" +
		"ACTION=add\x00" +
		"SUBSYSTEM=usb\x00" +
		"DEVTYPE=usb_interface\x00" +
		"INTERFACE=255/255/255\x00"

	fields := parseUevent([]byte(raw))
	if fields["DEVTYPE"] != "usb_interface" {
		t.Fatalf("DEVTYPE = %q, want usb_interface", fields["DEVTYPE"])
	}
	// handleUevent's own filter (SUBSYSTEM=usb && DEVTYPE=usb_device) is
	// what actually excludes this; this test just pins the field this repo's
	// own uevent samples showed real per-interface events carry, so a
	// future change to that filter has something concrete to check against.
}

// TestParseUeventEmpty pins the zero-value behavior for a malformed or
// truncated payload, which a real socket read should never actually
// deliver, but handleUevent must not panic on regardless.
func TestParseUeventEmpty(t *testing.T) {
	if fields := parseUevent(nil); len(fields) != 0 {
		t.Errorf("parseUevent(nil) = %v, want empty", fields)
	}
	if fields := parseUevent([]byte("garbage with no equals signs")); len(fields) != 0 {
		t.Errorf("parseUevent(garbage) = %v, want empty", fields)
	}
}

// TestLinuxHotplugSeenKey pins the "bus:addr" key format handleUevent uses
// to correlate an add event's DeviceList() lookup with the seen map a later
// remove event consults, since the two must agree exactly for removal to
// find anything.
func TestLinuxHotplugSeenKey(t *testing.T) {
	tests := []struct {
		bus, addr uint8
		want      string
	}{
		{1, 5, "1:5"},
		{0, 0, "0:0"},
		{255, 255, "255:255"},
	}
	for _, tt := range tests {
		if got := linuxHotplugSeenKey(tt.bus, tt.addr); got != tt.want {
			t.Errorf("linuxHotplugSeenKey(%d, %d) = %q, want %q", tt.bus, tt.addr, got, tt.want)
		}
	}
}

// TestLinuxHotplugHandleMatches pins the vendor/product filter semantics
// RegisterHotplugCallback documents: zero matches any vendor or product.
func TestLinuxHotplugHandleMatches(t *testing.T) {
	dev := &Device{Descriptor: DeviceDescriptor{VendorID: 0x0925, ProductID: 0x3881}}

	tests := []struct {
		name      string
		vid, pid  uint16
		wantMatch bool
	}{
		{"wildcard", 0, 0, true},
		{"vendor only, matches", 0x0925, 0, true},
		{"vendor only, mismatches", 0x1234, 0, false},
		{"vendor and product, matches", 0x0925, 0x3881, true},
		{"vendor matches, product mismatches", 0x0925, 0x1111, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &linuxHotplugHandle{vendorID: tt.vid, productID: tt.pid}
			if got := h.matches(dev); got != tt.wantMatch {
				t.Errorf("matches() = %v, want %v", got, tt.wantMatch)
			}
		})
	}
}
