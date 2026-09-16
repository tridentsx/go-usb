package usb

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// allPluginUUIDs returns the UUIDs used for IOKit plug-in access, by name.
func allPluginUUIDs() map[string][16]byte {
	return map[string][16]byte{
		"kIOUSBDeviceUserClientTypeID":    kIOUSBDeviceUserClientTypeID,
		"kIOUSBInterfaceUserClientTypeID": kIOUSBInterfaceUserClientTypeID,
		"kIOCFPlugInInterfaceID":          kIOCFPlugInInterfaceID,
		"kIOUSBDeviceInterfaceID":         kIOUSBDeviceInterfaceID,
	}
}

// TestIOCFPlugInInterfaceLayout pins the offsets used for vtable dispatch.
//
// A wrong offset here calls the wrong function pointer, which crashes rather
// than returning an error, so the layout is asserted rather than assumed. The C
// structure begins with IUNKNOWN_C_GUTS — a reserved pointer followed by
// QueryInterface, AddRef and Release — then two UInt16 fields, after which the
// next function pointer is realigned to 8 bytes.
func TestIOCFPlugInInterfaceLayout(t *testing.T) {
	var iface ioCFPlugInInterface

	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"_reserved", unsafe.Offsetof(iface._reserved), 0},
		{"QueryInterface", unsafe.Offsetof(iface.QueryInterface), 8},
		{"AddRef", unsafe.Offsetof(iface.AddRef), 16},
		{"Release", unsafe.Offsetof(iface.Release), 24},
		{"version", unsafe.Offsetof(iface.version), 32},
		{"revision", unsafe.Offsetof(iface.revision), 34},
		// version and revision occupy four bytes; the compiler must insert four
		// more so that Probe lands on an 8-byte boundary, exactly as C does.
		{"Probe", unsafe.Offsetof(iface.Probe), 40},
		{"Start", unsafe.Offsetof(iface.Start), 48},
		{"Stop", unsafe.Offsetof(iface.Stop), 56},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("offset of %s = %d, want %d", tt.name, tt.got, tt.want)
		}
	}

	if size := unsafe.Sizeof(iface); size != 64 {
		t.Errorf("sizeof(ioCFPlugInInterface) = %d, want 64", size)
	}
}

// TestUUIDBytesPreservesMemoryLayout checks the invariant that actually matters:
// the two words must reproduce the original byte sequence in order when written
// out, because that is the memory image CFUUIDBytes has when passed by value.
//
// The previous version of this test asserted a hand-computed big-endian
// constant, which verified that the code did what was intended rather than that
// the intention was right. It passed while the byte order was reversed, and the
// symptom was IOCreatePlugInInterfaceForService reporting kIOReturnUnsupported
// for every device.
func TestUUIDBytesPreservesMemoryLayout(t *testing.T) {
	for name, want := range allPluginUUIDs() {
		u := uuidBytes(want)

		var got [16]byte
		binary.LittleEndian.PutUint64(got[0:8], u.Lo)
		binary.LittleEndian.PutUint64(got[8:16], u.Hi)

		if got != want {
			t.Errorf("%s: round trip produced % x, want % x", name, got, want)
		}
	}
}

func TestUUIDWords(t *testing.T) {
	u := uuidBytes(kIOCFPlugInInterfaceID)
	lo, hi := u.words()
	if uintptr(u.Lo) != lo || uintptr(u.Hi) != hi {
		t.Error("words() did not return the struct fields in order")
	}
}

func TestCanonicalUUIDString(t *testing.T) {
	if got, want := canonicalUUIDString(kIOCFPlugInInterfaceID), "C244E858-109C-11D4-91D4-0050E4C6426F"; got != want {
		t.Errorf("kIOCFPlugInInterfaceID = %s, want %s", got, want)
	}
	if got, want := canonicalUUIDString(kIOUSBDeviceInterfaceID), "5C8187D0-9EF3-11D4-8B45-000A27052861"; got != want {
		t.Errorf("kIOUSBDeviceInterfaceID = %s, want %s", got, want)
	}
}

// TestUUIDsAreDistinct guards against a copy-paste error between the four UUIDs,
// which would silently query the wrong interface.
func TestUUIDsAreDistinct(t *testing.T) {
	uuids := map[string][16]byte{
		"kIOUSBDeviceUserClientTypeID":    kIOUSBDeviceUserClientTypeID,
		"kIOUSBInterfaceUserClientTypeID": kIOUSBInterfaceUserClientTypeID,
		"kIOCFPlugInInterfaceID":          kIOCFPlugInInterfaceID,
		"kIOUSBDeviceInterfaceID":         kIOUSBDeviceInterfaceID,
	}

	seen := make(map[[16]byte]string, len(uuids))
	for name, u := range uuids {
		if prev, dup := seen[u]; dup {
			t.Errorf("%s and %s have identical bytes", name, prev)
		}
		seen[u] = name

		var zero [16]byte
		if u == zero {
			t.Errorf("%s is all zeroes", name)
		}
	}
}

// TestUUIDVersionNibble is a sanity check on transcription. All four of these
// are version 1 UUIDs, so the high nibble of byte 6 is 1. A mistyped byte
// somewhere in the middle would very likely break this.
func TestUUIDVersionNibble(t *testing.T) {
	uuids := map[string][16]byte{
		"kIOUSBDeviceUserClientTypeID":    kIOUSBDeviceUserClientTypeID,
		"kIOUSBInterfaceUserClientTypeID": kIOUSBInterfaceUserClientTypeID,
		"kIOCFPlugInInterfaceID":          kIOCFPlugInInterfaceID,
		"kIOUSBDeviceInterfaceID":         kIOUSBDeviceInterfaceID,
	}

	for name, u := range uuids {
		if version := u[6] >> 4; version != 1 {
			t.Errorf("%s: UUID version nibble = %d, want 1", name, version)
		}
	}
}
