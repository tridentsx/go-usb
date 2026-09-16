package usb

import (
	"testing"
	"unsafe"
)

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
