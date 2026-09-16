package usb

import (
	"testing"
	"unsafe"
)

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

// TestUUIDBytes checks that the sixteen bytes as written in the headers survive
// conversion into the two machine words an ABI passes CFUUIDBytes in.
//
// The bytes are in network order, so byte 0 is the most significant of the first
// word. Getting this backwards would query a nonexistent interface, and
// QueryInterface would fail rather than crash — but silently, and every device
// would look unopenable.
func TestUUIDBytes(t *testing.T) {
	u := uuidBytes(kIOCFPlugInInterfaceID)

	if u.Lo != 0xC244E858109C11D4 {
		t.Errorf("Lo = %#016x, want 0xC244E858109C11D4", u.Lo)
	}
	if u.Hi != 0x91D40050E4C6426F {
		t.Errorf("Hi = %#016x, want 0x91D40050E4C6426F", u.Hi)
	}

	lo, hi := u.words()
	if uintptr(u.Lo) != lo || uintptr(u.Hi) != hi {
		t.Error("words() did not return the struct fields in order")
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
