package usb

import (
	"testing"
	"unsafe"
)

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
