package usb

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unsafe"
)

// --- Plug-in interface: UUIDs and the IOCFPlugInInterface table -----------

// allPluginUUIDs returns the UUIDs used for IOKit plug-in access, by name.
func allPluginUUIDs() map[string][16]byte {
	return map[string][16]byte{
		"kIOUSBDeviceUserClientTypeID":    kIOUSBDeviceUserClientTypeID,
		"kIOUSBInterfaceUserClientTypeID": kIOUSBInterfaceUserClientTypeID,
		"kIOCFPlugInInterfaceID":          kIOCFPlugInInterfaceID,
		"kIOUSBDeviceInterfaceID":         kIOUSBDeviceInterfaceID,
		"kIOUSBInterfaceInterfaceID300":   kIOUSBInterfaceInterfaceID300,
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
	if got, want := canonicalUUIDString(kIOUSBInterfaceInterfaceID300), "BCEAADDC-884D-4F27-8340-36D69FAB90F6"; got != want {
		t.Errorf("kIOUSBInterfaceInterfaceID300 = %s, want %s", got, want)
	}
}

// TestUUIDsAreDistinct guards against a copy-paste error between the UUIDs,
// which would silently query the wrong interface.
func TestUUIDsAreDistinct(t *testing.T) {
	uuids := allPluginUUIDs()

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

// TestUUIDVersionNibble is a sanity check on transcription. All of the
// original four are version 1 UUIDs, so the high nibble of byte 6 is 1; a
// mistyped byte somewhere in the middle would very likely break this.
// kIOUSBInterfaceInterfaceID300 is version 4 (byte 6 high nibble 4), which is
// itself worth pinning: getting that nibble wrong is exactly the kind of
// transcription slip this test exists to catch.
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

	if version := kIOUSBInterfaceInterfaceID300[6] >> 4; version != 4 {
		t.Errorf("kIOUSBInterfaceInterfaceID300: UUID version nibble = %d, want 4", version)
	}
}

// TestUUIDRoundTripsThroughCoreFoundation asks CoreFoundation what UUID it built
// from our raw bytes and compares it against the canonical text form.
//
// This is the check that catches a byte-order mistake. Passing the sixteen bytes
// in the wrong order produces a valid CFUUID for a *different* UUID, so creation
// succeeds and the error only surfaces later as
// IOCreatePlugInInterfaceForService reporting kIOReturnUnsupported for every
// device — which is exactly what happened before this test existed.
func TestUUIDRoundTripsThroughCoreFoundation(t *testing.T) {
	k, err := loadIOKit()
	if err != nil {
		t.Fatalf("loadIOKit: %v", err)
	}
	if iokitPlugin.CFUUIDCreateFromUUIDBytes == nil || iokitPlugin.CFUUIDCreateString == nil {
		t.Fatal("CFUUID entry points not resolved")
	}

	for name, raw := range allPluginUUIDs() {
		uuid := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(raw))
		if uuid == 0 {
			t.Errorf("%s: CFUUIDCreateFromUUIDBytes returned NULL", name)
			continue
		}

		str := iokitPlugin.CFUUIDCreateString(0, uuid)
		if str == 0 {
			t.Errorf("%s: CFUUIDCreateString returned NULL", name)
			k.CFRelease(uuid)
			continue
		}

		buf := make([]byte, 64)
		if !k.CFStringGetCString(str, &buf[0], int32(len(buf)), kCFStringEncodingUTF8) {
			t.Errorf("%s: CFStringGetCString failed", name)
		} else {
			got := ""
			for i, b := range buf {
				if b == 0 {
					got = string(buf[:i])
					break
				}
			}
			if want := canonicalUUIDString(raw); got != want {
				t.Errorf("%s: CoreFoundation reports %s, want %s", name, got, want)
			} else {
				t.Logf("%s = %s", name, got)
			}
		}

		k.CFRelease(str)
		k.CFRelease(uuid)
	}
}

// --- IOUSBDeviceInterface method table -------------------------------------

// TestIOUSBDeviceInterfaceLayout pins every method offset in the table used for
// vtable dispatch.
//
// The C structure is a flat sequence of function pointers after IUNKNOWN_C_GUTS,
// so each method sits at eight times its index on a 64-bit platform. Asserting
// the indices explicitly means a field inserted, removed or reordered by mistake
// fails here rather than by calling the wrong function at runtime.
func TestIOUSBDeviceInterfaceLayout(t *testing.T) {
	var d ioUSBDeviceInterface

	// index -> field offset, in declaration order from IOUSBLib.h.
	expected := []struct {
		index int
		name  string
		got   uintptr
	}{
		{0, "_reserved", unsafe.Offsetof(d._reserved)},
		{1, "QueryInterface", unsafe.Offsetof(d.QueryInterface)},
		{2, "AddRef", unsafe.Offsetof(d.AddRef)},
		{3, "Release", unsafe.Offsetof(d.Release)},
		{4, "CreateDeviceAsyncEventSource", unsafe.Offsetof(d.CreateDeviceAsyncEventSource)},
		{5, "GetDeviceAsyncEventSource", unsafe.Offsetof(d.GetDeviceAsyncEventSource)},
		{6, "CreateDeviceAsyncPort", unsafe.Offsetof(d.CreateDeviceAsyncPort)},
		{7, "GetDeviceAsyncPort", unsafe.Offsetof(d.GetDeviceAsyncPort)},
		{8, "USBDeviceOpen", unsafe.Offsetof(d.USBDeviceOpen)},
		{9, "USBDeviceClose", unsafe.Offsetof(d.USBDeviceClose)},
		{10, "GetDeviceClass", unsafe.Offsetof(d.GetDeviceClass)},
		{11, "GetDeviceSubClass", unsafe.Offsetof(d.GetDeviceSubClass)},
		{12, "GetDeviceProtocol", unsafe.Offsetof(d.GetDeviceProtocol)},
		{13, "GetDeviceVendor", unsafe.Offsetof(d.GetDeviceVendor)},
		{14, "GetDeviceProduct", unsafe.Offsetof(d.GetDeviceProduct)},
		{15, "GetDeviceReleaseNumber", unsafe.Offsetof(d.GetDeviceReleaseNumber)},
		{16, "GetDeviceAddress", unsafe.Offsetof(d.GetDeviceAddress)},
		{17, "GetDeviceBusPowerAvailable", unsafe.Offsetof(d.GetDeviceBusPowerAvailable)},
		{18, "GetDeviceSpeed", unsafe.Offsetof(d.GetDeviceSpeed)},
		{19, "GetNumberOfConfigurations", unsafe.Offsetof(d.GetNumberOfConfigurations)},
		{20, "GetLocationID", unsafe.Offsetof(d.GetLocationID)},
		{21, "GetConfigurationDescriptorPtr", unsafe.Offsetof(d.GetConfigurationDescriptorPtr)},
		{22, "GetConfiguration", unsafe.Offsetof(d.GetConfiguration)},
		{23, "SetConfiguration", unsafe.Offsetof(d.SetConfiguration)},
		{24, "GetBusFrameNumber", unsafe.Offsetof(d.GetBusFrameNumber)},
		{25, "ResetDevice", unsafe.Offsetof(d.ResetDevice)},
		{26, "DeviceRequest", unsafe.Offsetof(d.DeviceRequest)},
		{27, "DeviceRequestAsync", unsafe.Offsetof(d.DeviceRequestAsync)},
		{28, "CreateInterfaceIterator", unsafe.Offsetof(d.CreateInterfaceIterator)},
	}

	ptrSize := unsafe.Sizeof(uintptr(0))
	for _, e := range expected {
		if want := uintptr(e.index) * ptrSize; e.got != want {
			t.Errorf("%s is at offset %d (index %d), want offset %d (index %d)",
				e.name, e.got, e.got/ptrSize, want, e.index)
		}
	}

	if size, want := unsafe.Sizeof(d), uintptr(len(expected))*ptrSize; size != want {
		t.Errorf("sizeof(ioUSBDeviceInterface) = %d, want %d; a field was added or removed", size, want)
	}
}

// TestIOUSBDeviceInterfaceSharesIUnknownLayout checks that the device interface
// begins with the same IUnknown fields as the plug-in interface.
//
// Every IOKit COM-style interface starts with IUNKNOWN_C_GUTS, which is what
// makes it safe to call Release through either table.
func TestIOUSBDeviceInterfaceSharesIUnknownLayout(t *testing.T) {
	var d ioUSBDeviceInterface
	var p ioCFPlugInInterface

	pairs := []struct {
		name        string
		dev, plugin uintptr
	}{
		{"_reserved", unsafe.Offsetof(d._reserved), unsafe.Offsetof(p._reserved)},
		{"QueryInterface", unsafe.Offsetof(d.QueryInterface), unsafe.Offsetof(p.QueryInterface)},
		{"AddRef", unsafe.Offsetof(d.AddRef), unsafe.Offsetof(p.AddRef)},
		{"Release", unsafe.Offsetof(d.Release), unsafe.Offsetof(p.Release)},
	}

	for _, pair := range pairs {
		if pair.dev != pair.plugin {
			t.Errorf("%s: device interface offset %d, plug-in interface offset %d",
				pair.name, pair.dev, pair.plugin)
		}
	}
}

func TestIOKitSpeedToSpeed(t *testing.T) {
	tests := map[uint8]Speed{
		kUSBDeviceSpeedLow:   SpeedLow,
		kUSBDeviceSpeedFull:  SpeedFull,
		kUSBDeviceSpeedHigh:  SpeedHigh,
		kUSBDeviceSpeedSuper: SpeedSuper,
		0xff:                 SpeedUnknown,
	}
	for in, want := range tests {
		if got := iokitSpeedToSpeed(in); got != want {
			t.Errorf("iokitSpeedToSpeed(%d) = %v, want %v", in, got, want)
		}
	}
}

// --- IOUSBInterfaceInterface300 method table, and its request structures --

// TestIOUSBInterfaceInterfaceLayout pins every method offset in the table used
// for vtable dispatch, the same discipline as
// TestIOUSBDeviceInterfaceLayout.
func TestIOUSBInterfaceInterfaceLayout(t *testing.T) {
	var d ioUSBInterfaceInterface300

	// index -> field offset, in declaration order from IOUSBLib.h.
	expected := []struct {
		index int
		name  string
		got   uintptr
	}{
		{0, "_reserved", unsafe.Offsetof(d._reserved)},
		{1, "QueryInterface", unsafe.Offsetof(d.QueryInterface)},
		{2, "AddRef", unsafe.Offsetof(d.AddRef)},
		{3, "Release", unsafe.Offsetof(d.Release)},
		{4, "CreateInterfaceAsyncEventSource", unsafe.Offsetof(d.CreateInterfaceAsyncEventSource)},
		{5, "GetInterfaceAsyncEventSource", unsafe.Offsetof(d.GetInterfaceAsyncEventSource)},
		{6, "CreateInterfaceAsyncPort", unsafe.Offsetof(d.CreateInterfaceAsyncPort)},
		{7, "GetInterfaceAsyncPort", unsafe.Offsetof(d.GetInterfaceAsyncPort)},
		{8, "USBInterfaceOpen", unsafe.Offsetof(d.USBInterfaceOpen)},
		{9, "USBInterfaceClose", unsafe.Offsetof(d.USBInterfaceClose)},
		{10, "GetInterfaceClass", unsafe.Offsetof(d.GetInterfaceClass)},
		{11, "GetInterfaceSubClass", unsafe.Offsetof(d.GetInterfaceSubClass)},
		{12, "GetInterfaceProtocol", unsafe.Offsetof(d.GetInterfaceProtocol)},
		{13, "GetDeviceVendor", unsafe.Offsetof(d.GetDeviceVendor)},
		{14, "GetDeviceProduct", unsafe.Offsetof(d.GetDeviceProduct)},
		{15, "GetDeviceReleaseNumber", unsafe.Offsetof(d.GetDeviceReleaseNumber)},
		{16, "GetConfigurationValue", unsafe.Offsetof(d.GetConfigurationValue)},
		{17, "GetInterfaceNumber", unsafe.Offsetof(d.GetInterfaceNumber)},
		{18, "GetAlternateSetting", unsafe.Offsetof(d.GetAlternateSetting)},
		{19, "GetNumEndpoints", unsafe.Offsetof(d.GetNumEndpoints)},
		{20, "GetLocationID", unsafe.Offsetof(d.GetLocationID)},
		{21, "GetDevice", unsafe.Offsetof(d.GetDevice)},
		{22, "SetAlternateInterface", unsafe.Offsetof(d.SetAlternateInterface)},
		{23, "GetBusFrameNumber", unsafe.Offsetof(d.GetBusFrameNumber)},
		{24, "ControlRequest", unsafe.Offsetof(d.ControlRequest)},
		{25, "ControlRequestAsync", unsafe.Offsetof(d.ControlRequestAsync)},
		{26, "GetPipeProperties", unsafe.Offsetof(d.GetPipeProperties)},
		{27, "GetPipeStatus", unsafe.Offsetof(d.GetPipeStatus)},
		{28, "AbortPipe", unsafe.Offsetof(d.AbortPipe)},
		{29, "ResetPipe", unsafe.Offsetof(d.ResetPipe)},
		{30, "ClearPipeStall", unsafe.Offsetof(d.ClearPipeStall)},
		{31, "ReadPipe", unsafe.Offsetof(d.ReadPipe)},
		{32, "WritePipe", unsafe.Offsetof(d.WritePipe)},
		{33, "ReadPipeAsync", unsafe.Offsetof(d.ReadPipeAsync)},
		{34, "WritePipeAsync", unsafe.Offsetof(d.WritePipeAsync)},
		{35, "ReadIsochPipeAsync", unsafe.Offsetof(d.ReadIsochPipeAsync)},
		{36, "WriteIsochPipeAsync", unsafe.Offsetof(d.WriteIsochPipeAsync)},
		{37, "ControlRequestTO", unsafe.Offsetof(d.ControlRequestTO)},
		{38, "ControlRequestAsyncTO", unsafe.Offsetof(d.ControlRequestAsyncTO)},
		{39, "ReadPipeTO", unsafe.Offsetof(d.ReadPipeTO)},
		{40, "WritePipeTO", unsafe.Offsetof(d.WritePipeTO)},
		{41, "ReadPipeAsyncTO", unsafe.Offsetof(d.ReadPipeAsyncTO)},
		{42, "WritePipeAsyncTO", unsafe.Offsetof(d.WritePipeAsyncTO)},
		{43, "USBInterfaceGetStringIndex", unsafe.Offsetof(d.USBInterfaceGetStringIndex)},
		{44, "USBInterfaceOpenSeize", unsafe.Offsetof(d.USBInterfaceOpenSeize)},
		{45, "ClearPipeStallBothEnds", unsafe.Offsetof(d.ClearPipeStallBothEnds)},
		{46, "SetPipePolicy", unsafe.Offsetof(d.SetPipePolicy)},
		{47, "GetBandwidthAvailable", unsafe.Offsetof(d.GetBandwidthAvailable)},
		{48, "GetEndpointProperties", unsafe.Offsetof(d.GetEndpointProperties)},
		{49, "LowLatencyReadIsochPipeAsync", unsafe.Offsetof(d.LowLatencyReadIsochPipeAsync)},
		{50, "LowLatencyWriteIsochPipeAsync", unsafe.Offsetof(d.LowLatencyWriteIsochPipeAsync)},
		{51, "LowLatencyCreateBuffer", unsafe.Offsetof(d.LowLatencyCreateBuffer)},
		{52, "LowLatencyDestroyBuffer", unsafe.Offsetof(d.LowLatencyDestroyBuffer)},
		{53, "GetBusMicroFrameNumber", unsafe.Offsetof(d.GetBusMicroFrameNumber)},
		{54, "GetFrameListTime", unsafe.Offsetof(d.GetFrameListTime)},
		{55, "GetIOUSBLibVersion", unsafe.Offsetof(d.GetIOUSBLibVersion)},
		{56, "FindNextAssociatedDescriptor", unsafe.Offsetof(d.FindNextAssociatedDescriptor)},
		{57, "FindNextAltInterface", unsafe.Offsetof(d.FindNextAltInterface)},
		{58, "GetBusFrameNumberWithTime", unsafe.Offsetof(d.GetBusFrameNumberWithTime)},
	}

	ptrSize := unsafe.Sizeof(uintptr(0))
	for _, e := range expected {
		if want := uintptr(e.index) * ptrSize; e.got != want {
			t.Errorf("%s is at offset %d (index %d), want offset %d (index %d)",
				e.name, e.got, e.got/ptrSize, want, e.index)
		}
	}

	if size, want := unsafe.Sizeof(d), uintptr(len(expected))*ptrSize; size != want {
		t.Errorf("sizeof(ioUSBInterfaceInterface300) = %d, want %d; a field was added or removed", size, want)
	}
}

// TestIOUSBInterfaceInterfaceSharesIUnknownLayout checks that the interface
// interface begins with the same IUnknown fields as the plug-in interface, the
// same invariant TestIOUSBDeviceInterfaceSharesIUnknownLayout checks for the
// device interface.
func TestIOUSBInterfaceInterfaceSharesIUnknownLayout(t *testing.T) {
	var d ioUSBInterfaceInterface300
	var p ioCFPlugInInterface

	pairs := []struct {
		name        string
		iface, plug uintptr
	}{
		{"_reserved", unsafe.Offsetof(d._reserved), unsafe.Offsetof(p._reserved)},
		{"QueryInterface", unsafe.Offsetof(d.QueryInterface), unsafe.Offsetof(p.QueryInterface)},
		{"AddRef", unsafe.Offsetof(d.AddRef), unsafe.Offsetof(p.AddRef)},
		{"Release", unsafe.Offsetof(d.Release), unsafe.Offsetof(p.Release)},
	}

	for _, pair := range pairs {
		if pair.iface != pair.plug {
			t.Errorf("%s: interface interface offset %d, plug-in interface offset %d",
				pair.name, pair.iface, pair.plug)
		}
	}
}

// TestIOUSBFindInterfaceRequestLayout pins the four-UInt16 layout
// CreateInterfaceIterator's request parameter needs, matching
// IOUSBFindInterfaceRequest in IOKit/usb/USB.h exactly (no padding: four
// naturally-aligned UInt16 fields already total 8 bytes).
func TestIOUSBFindInterfaceRequestLayout(t *testing.T) {
	var r ioUSBFindInterfaceRequest

	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"bInterfaceClass", unsafe.Offsetof(r.bInterfaceClass), 0},
		{"bInterfaceSubClass", unsafe.Offsetof(r.bInterfaceSubClass), 2},
		{"bInterfaceProtocol", unsafe.Offsetof(r.bInterfaceProtocol), 4},
		{"bAlternateSetting", unsafe.Offsetof(r.bAlternateSetting), 6},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("offset of %s = %d, want %d", tt.name, tt.got, tt.want)
		}
	}

	if size := unsafe.Sizeof(r); size != 8 {
		t.Errorf("sizeof(ioUSBFindInterfaceRequest) = %d, want 8", size)
	}
}

// TestIOUSBIsocFrameLayout pins the layout ReadIsochPipeAsync and
// WriteIsochPipeAsync require for their frame list argument.
func TestIOUSBIsocFrameLayout(t *testing.T) {
	var f ioUSBIsocFrame

	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"frStatus", unsafe.Offsetof(f.frStatus), 0},
		{"frReqCount", unsafe.Offsetof(f.frReqCount), 4},
		{"frActCount", unsafe.Offsetof(f.frActCount), 6},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("offset of %s = %d, want %d", tt.name, tt.got, tt.want)
		}
	}

	if size := unsafe.Sizeof(f); size != 8 {
		t.Errorf("sizeof(ioUSBIsocFrame) = %d, want 8", size)
	}
}

// --- IOUSBDevRequest ---------------------------------------------------

// TestIOUSBDevRequestLayout pins the layout IOKit's DeviceRequest expects.
//
// A wrong offset here does not crash: it sends a control transfer with the wrong
// request type, value or length, which either fails against the device or, worse,
// succeeds as a different request than intended.
func TestIOUSBDevRequestLayout(t *testing.T) {
	var r ioUSBDevRequest

	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"bmRequestType", unsafe.Offsetof(r.bmRequestType), 0},
		{"bRequest", unsafe.Offsetof(r.bRequest), 1},
		{"wValue", unsafe.Offsetof(r.wValue), 2},
		{"wIndex", unsafe.Offsetof(r.wIndex), 4},
		{"wLength", unsafe.Offsetof(r.wLength), 6},
		{"pData", unsafe.Offsetof(r.pData), 8},
		{"wLenDone", unsafe.Offsetof(r.wLenDone), 16},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("offset of %s = %d, want %d", tt.name, tt.got, tt.want)
		}
	}

	if size := unsafe.Sizeof(r); size != 24 {
		t.Errorf("sizeof(ioUSBDevRequest) = %d, want 24", size)
	}
}

// --- locationID handling -----------------------------------------------

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

// TestIOKitDevicePath pins the "iokit:<hex locationID>" path format that
// IsValidDevicePath parses.
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
