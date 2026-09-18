package usb

import (
	"encoding/binary"
	"fmt"
)

// IOKit's low-level struct layouts, constants and small pure-Go helpers: the
// plug-in and USB device/interface method tables, the structures IOKit's
// calls take or fill in, and locationID/UUID handling. None of this has
// independent behavior of its own — device_interface_darwin.go,
// interface_darwin.go and vtable_darwin.go are what actually dispatch
// through these tables — it is grouped here because splitting each struct
// into its own file made the darwin file list harder to navigate than the
// structs are individually complex.
//
// A wrong offset in any of the method tables calls the wrong function
// pointer, which crashes rather than returning an error, and the cause can
// be several calls upstream of the symptom. The layout and UUID tests in
// iokit_types_darwin_test.go assert every offset and value against the real
// C structures and canonical UUID strings rather than assuming the Go
// layout matches — the same discipline that caught the CFUUIDBytes
// byte-order bug below.

// --- Plug-in interface: UUIDs and the IOCFPlugInInterface table -----------

// cfUUIDBytes mirrors CoreFoundation's CFUUIDBytes: sixteen raw bytes.
//
// It is passed to QueryInterface *by value*. On both arm64 and amd64 a 16-byte
// all-integer struct is passed in two consecutive registers, so it decomposes
// into exactly two machine words in the same order on either architecture.
type cfUUIDBytes struct {
	Lo uint64
	Hi uint64
}

// words returns the struct as the two machine words an ABI passes it in.
func (u cfUUIDBytes) words() (uintptr, uintptr) {
	return uintptr(u.Lo), uintptr(u.Hi)
}

// uuidBytes builds a cfUUIDBytes from the sixteen bytes as written in
// IOUSBLib.h.
//
// CFUUIDBytes is a structure of sixteen UInt8 fields, byte0 at offset 0 through
// byte15 at offset 15. Passing it by value therefore requires two words whose
// *memory image* is that byte sequence in order, which on a little-endian
// machine means decoding little-endian, not big-endian.
//
// Getting this backwards does not fail loudly: CFUUIDCreateFromUUIDBytes
// accepts the reversed bytes and returns a perfectly valid object for a
// different UUID, after which IOCreatePlugInInterfaceForService matches nothing
// and reports kIOReturnUnsupported. TestUUIDRoundTripsThroughCoreFoundation
// checks the resulting UUID against its canonical string to catch exactly that.
func uuidBytes(b [16]byte) cfUUIDBytes {
	return cfUUIDBytes{
		Lo: binary.LittleEndian.Uint64(b[0:8]),
		Hi: binary.LittleEndian.Uint64(b[8:16]),
	}
}

// canonicalUUIDString renders the sixteen bytes in the standard textual form,
// which is what CoreFoundation should report for the same UUID.
func canonicalUUIDString(b [16]byte) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hex[v>>4], hex[v&0x0f])
	}
	return string(out)
}

// IOKit plug-in and interface UUIDs, transcribed from IOKit/usb/IOUSBLib.h and
// IOKit/IOCFPlugIn.h.
var (
	// kIOUSBDeviceUserClientTypeID identifies the plug-in type for a USB device.
	kIOUSBDeviceUserClientTypeID = [16]byte{
		0x9d, 0xc7, 0xb7, 0x80, 0x9e, 0xc0, 0x11, 0xD4,
		0xa5, 0x4f, 0x00, 0x0a, 0x27, 0x05, 0x28, 0x61,
	}

	// kIOUSBInterfaceUserClientTypeID identifies the plug-in type for a USB
	// interface.
	kIOUSBInterfaceUserClientTypeID = [16]byte{
		0x2d, 0x97, 0x86, 0xc6, 0x9e, 0xf3, 0x11, 0xD4,
		0xad, 0x51, 0x00, 0x0a, 0x27, 0x05, 0x28, 0x61,
	}

	// kIOCFPlugInInterfaceID is the interface every IOKit plug-in exposes.
	kIOCFPlugInInterfaceID = [16]byte{
		0xC2, 0x44, 0xE8, 0x58, 0x10, 0x9C, 0x11, 0xD4,
		0x91, 0xD4, 0x00, 0x50, 0xE4, 0xC6, 0x42, 0x6F,
	}

	// kIOUSBDeviceInterfaceID is the base USB device interface.
	kIOUSBDeviceInterfaceID = [16]byte{
		0x5c, 0x81, 0x87, 0xd0, 0x9e, 0xf3, 0x11, 0xD4,
		0x8b, 0x45, 0x00, 0x0a, 0x27, 0x05, 0x28, 0x61,
	}

	// kIOUSBInterfaceInterfaceID300 is the USB interface COM interface version
	// used for interface claiming and bulk/interrupt transfers. Later versions
	// (398 and up) only add methods this package does not use.
	kIOUSBInterfaceInterfaceID300 = [16]byte{
		0xBC, 0xEA, 0xAD, 0xDC, 0x88, 0x4D, 0x4F, 0x27,
		0x83, 0x40, 0x36, 0xD6, 0x9F, 0xAB, 0x90, 0xF6,
	}
)

// ioCFPlugInInterface mirrors IOCFPlugInInterface from IOKit/IOCFPlugIn.h.
//
//	typedef struct IOCFPlugInInterfaceStruct {
//	    IUNKNOWN_C_GUTS;      // void *_reserved, QueryInterface, AddRef, Release
//	    UInt16 version;
//	    UInt16 revision;
//	    IOReturn (*Probe)(void *, CFDictionaryRef, io_service_t, SInt32 *);
//	    IOReturn (*Start)(void *, CFDictionaryRef, io_service_t);
//	    IOReturn (*Stop)(void *);
//	} IOCFPlugInInterface;
//
// Only the IUnknown portion is used: QueryInterface to obtain the USB device
// interface, and Release to drop the plug-in. Probe, Start and Stop are declared
// so that the structure's size matches the C one, which the tests assert.
type ioCFPlugInInterface struct {
	_reserved      uintptr
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	version        uint16
	revision       uint16
	Probe          uintptr
	Start          uintptr
	Stop           uintptr
}

// --- IOUSBDeviceInterface method table -------------------------------------

// ioUSBDeviceInterface method table.
//
// The order is the declaration order in IOKit/usb/IOUSBLib.h and it *is* the
// ABI: a wrong offset calls the wrong function, which crashes rather than
// returning an error, and the cause can be several calls upstream of the
// symptom.
//
// Two things make that risk manageable. The offsets are asserted against the C
// structure below, and the methods first used are getters whose answers are
// already known from the IOKit registry, so the table is checked against an
// external oracle rather than against the assumption that produced it. See
// TestDeviceInterfaceAgreesWithRegistry.
//
//	typedef struct IOUSBDeviceStruct {
//	    IUNKNOWN_C_GUTS;                                  // _reserved, QueryInterface, AddRef, Release
//	    IOReturn (*CreateDeviceAsyncEventSource)(void *, CFRunLoopSourceRef *);
//	    CFRunLoopSourceRef (*GetDeviceAsyncEventSource)(void *);
//	    IOReturn (*CreateDeviceAsyncPort)(void *, mach_port_t *);
//	    mach_port_t (*GetDeviceAsyncPort)(void *);
//	    IOReturn (*USBDeviceOpen)(void *);
//	    IOReturn (*USBDeviceClose)(void *);
//	    IOReturn (*GetDeviceClass)(void *, UInt8 *);
//	    IOReturn (*GetDeviceSubClass)(void *, UInt8 *);
//	    IOReturn (*GetDeviceProtocol)(void *, UInt8 *);
//	    IOReturn (*GetDeviceVendor)(void *, UInt16 *);
//	    IOReturn (*GetDeviceProduct)(void *, UInt16 *);
//	    IOReturn (*GetDeviceReleaseNumber)(void *, UInt16 *);
//	    IOReturn (*GetDeviceAddress)(void *, USBDeviceAddress *);
//	    IOReturn (*GetDeviceBusPowerAvailable)(void *, UInt32 *);
//	    IOReturn (*GetDeviceSpeed)(void *, UInt8 *);
//	    IOReturn (*GetNumberOfConfigurations)(void *, UInt8 *);
//	    IOReturn (*GetLocationID)(void *, UInt32 *);
//	    IOReturn (*GetConfigurationDescriptorPtr)(void *, UInt8, IOUSBConfigurationDescriptorPtr *);
//	    IOReturn (*GetConfiguration)(void *, UInt8 *);
//	    IOReturn (*SetConfiguration)(void *, UInt8);
//	    IOReturn (*GetBusFrameNumber)(void *, UInt64 *, AbsoluteTime *);
//	    IOReturn (*ResetDevice)(void *);
//	    IOReturn (*DeviceRequest)(void *, IOUSBDevRequest *);
//	    IOReturn (*DeviceRequestAsync)(void *, IOUSBDevRequest *, IOAsyncCallback1, void *);
//	    IOReturn (*CreateInterfaceIterator)(void *, IOUSBFindInterfaceRequest *, io_iterator_t *);
//	} IOUSBDeviceInterface;
type ioUSBDeviceInterface struct {
	_reserved      uintptr
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr

	CreateDeviceAsyncEventSource  uintptr
	GetDeviceAsyncEventSource     uintptr
	CreateDeviceAsyncPort         uintptr
	GetDeviceAsyncPort            uintptr
	USBDeviceOpen                 uintptr
	USBDeviceClose                uintptr
	GetDeviceClass                uintptr
	GetDeviceSubClass             uintptr
	GetDeviceProtocol             uintptr
	GetDeviceVendor               uintptr
	GetDeviceProduct              uintptr
	GetDeviceReleaseNumber        uintptr
	GetDeviceAddress              uintptr
	GetDeviceBusPowerAvailable    uintptr
	GetDeviceSpeed                uintptr
	GetNumberOfConfigurations     uintptr
	GetLocationID                 uintptr
	GetConfigurationDescriptorPtr uintptr
	GetConfiguration              uintptr
	SetConfiguration              uintptr
	GetBusFrameNumber             uintptr
	ResetDevice                   uintptr
	DeviceRequest                 uintptr
	DeviceRequestAsync            uintptr
	CreateInterfaceIterator       uintptr
}

// IOKit device speed values, as reported by GetDeviceSpeed.
const (
	kUSBDeviceSpeedLow   = 0
	kUSBDeviceSpeedFull  = 1
	kUSBDeviceSpeedHigh  = 2
	kUSBDeviceSpeedSuper = 3
)

// iokitSpeedToSpeed maps an IOKit device speed onto the portable Speed type.
func iokitSpeedToSpeed(s uint8) Speed {
	switch s {
	case kUSBDeviceSpeedLow:
		return SpeedLow
	case kUSBDeviceSpeedFull:
		return SpeedFull
	case kUSBDeviceSpeedHigh:
		return SpeedHigh
	case kUSBDeviceSpeedSuper:
		return SpeedSuper
	default:
		return SpeedUnknown
	}
}

// --- IOUSBInterfaceInterface300 method table, and its request structures --

// ioUSBInterfaceInterface300 method table, and the request structure used to
// find one.
//
// Untagged the same way ioUSBDeviceInterface above is: a wrong offset here
// calls the wrong function, which crashes rather than returning an error.
//
//	typedef struct IOUSBInterfaceStruct300
//	{
//	    IUNKNOWN_C_GUTS;                                     // _reserved, QueryInterface, AddRef, Release
//	    IOReturn (* CreateInterfaceAsyncEventSource)(void*, CFRunLoopSourceRef*);
//	    CFRunLoopSourceRef (* GetInterfaceAsyncEventSource)(void*);
//	    IOReturn (* CreateInterfaceAsyncPort)(void*, mach_port_t*);
//	    mach_port_t (* GetInterfaceAsyncPort)(void*);
//	    IOReturn (* USBInterfaceOpen)(void*);
//	    IOReturn (* USBInterfaceClose)(void*);
//	    IOReturn (* GetInterfaceClass)(void*, UInt8*);
//	    IOReturn (* GetInterfaceSubClass)(void*, UInt8*);
//	    IOReturn (* GetInterfaceProtocol)(void*, UInt8*);
//	    IOReturn (* GetDeviceVendor)(void*, UInt16*);
//	    IOReturn (* GetDeviceProduct)(void*, UInt16*);
//	    IOReturn (* GetDeviceReleaseNumber)(void*, UInt16*);
//	    IOReturn (* GetConfigurationValue)(void*, UInt8*);
//	    IOReturn (* GetInterfaceNumber)(void*, UInt8*);
//	    IOReturn (* GetAlternateSetting)(void*, UInt8*);
//	    IOReturn (* GetNumEndpoints)(void*, UInt8*);
//	    IOReturn (* GetLocationID)(void*, UInt32*);
//	    IOReturn (* GetDevice)(void*, io_service_t*);
//	    IOReturn (* SetAlternateInterface)(void*, UInt8);
//	    IOReturn (* GetBusFrameNumber)(void*, UInt64*, AbsoluteTime*);
//	    IOReturn (* ControlRequest)(void*, UInt8, IOUSBDevRequest*);
//	    IOReturn (* ControlRequestAsync)(void*, UInt8, IOUSBDevRequest*, IOAsyncCallback1, void*);
//	    IOReturn (* GetPipeProperties)(void*, UInt8, UInt8*, UInt8*, UInt8*, UInt16*, UInt8*);
//	    IOReturn (* GetPipeStatus)(void*, UInt8);
//	    IOReturn (* AbortPipe)(void*, UInt8);
//	    IOReturn (* ResetPipe)(void*, UInt8);
//	    IOReturn (* ClearPipeStall)(void*, UInt8);
//	    IOReturn (* ReadPipe)(void*, UInt8, void*, UInt32*);
//	    IOReturn (* WritePipe)(void*, UInt8, void*, UInt32);
//	    IOReturn (* ReadPipeAsync)(void*, UInt8, void*, UInt32, IOAsyncCallback1, void*);
//	    IOReturn (* WritePipeAsync)(void*, UInt8, void*, UInt32, IOAsyncCallback1, void*);
//	    IOReturn (* ReadIsochPipeAsync)(void*, UInt8, void*, UInt64, UInt32, IOUSBIsocFrame*, IOAsyncCallback1, void*);
//	    IOReturn (* WriteIsochPipeAsync)(void*, UInt8, void*, UInt64, UInt32, IOUSBIsocFrame*, IOAsyncCallback1, void*);
//	    IOReturn (* ControlRequestTO)(void*, UInt8, IOUSBDevRequestTO*);
//	    IOReturn (* ControlRequestAsyncTO)(void*, UInt8, IOUSBDevRequestTO*, IOAsyncCallback1, void*);
//	    IOReturn (* ReadPipeTO)(void*, UInt8, void*, UInt32*, UInt32, UInt32);
//	    IOReturn (* WritePipeTO)(void*, UInt8, void*, UInt32, UInt32, UInt32);
//	    IOReturn (* ReadPipeAsyncTO)(void*, UInt8, void*, UInt32, UInt32, UInt32, IOAsyncCallback1, void*);
//	    IOReturn (* WritePipeAsyncTO)(void*, UInt8, void*, UInt32, UInt32, UInt32, IOAsyncCallback1, void*);
//	    IOReturn (* USBInterfaceGetStringIndex)(void*, UInt8*);
//	    IOReturn (* USBInterfaceOpenSeize)(void*);
//	    IOReturn (* ClearPipeStallBothEnds)(void*, UInt8);
//	    IOReturn (* SetPipePolicy)(void*, UInt8, UInt16, UInt8);
//	    IOReturn (* GetBandwidthAvailable)(void*, UInt32*);
//	    IOReturn (* GetEndpointProperties)(void*, UInt8, UInt8, UInt8, UInt8*, UInt16*, UInt8*);
//	    IOReturn (* LowLatencyReadIsochPipeAsync)(void*, UInt8, void*, UInt64, UInt32, UInt32, IOUSBLowLatencyIsocFrame*, IOAsyncCallback1, void*);
//	    IOReturn (* LowLatencyWriteIsochPipeAsync)(void*, UInt8, void*, UInt64, UInt32, UInt32, IOUSBLowLatencyIsocFrame*, IOAsyncCallback1, void*);
//	    IOReturn (* LowLatencyCreateBuffer)(void*, void**, IOByteCount, UInt32);
//	    IOReturn (* LowLatencyDestroyBuffer)(void*, void*);
//	    IOReturn (* GetBusMicroFrameNumber)(void*, UInt64*, AbsoluteTime*);
//	    IOReturn (* GetFrameListTime)(void*, UInt32*);
//	    IOReturn (* GetIOUSBLibVersion)(void*, NumVersion*, NumVersion*);
//	    IOUSBDescriptorHeader* (*FindNextAssociatedDescriptor)(void*, const void*, UInt8);
//	    IOUSBDescriptorHeader* (*FindNextAltInterface)(void*, const void*, IOUSBFindInterfaceRequest*);
//	    IOReturn (* GetBusFrameNumberWithTime)(void*, UInt64*, AbsoluteTime*);
//	} IOUSBInterfaceInterface300;
//
// Every field through GetBusFrameNumberWithTime is transcribed, even the ones
// this package does not call yet, so the offsets after them and the overall
// size stay correct and testable against the real structure. See
// TestIOUSBInterfaceInterfaceLayout.
type ioUSBInterfaceInterface300 struct {
	_reserved      uintptr
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr

	CreateInterfaceAsyncEventSource uintptr
	GetInterfaceAsyncEventSource    uintptr
	CreateInterfaceAsyncPort        uintptr
	GetInterfaceAsyncPort           uintptr
	USBInterfaceOpen                uintptr
	USBInterfaceClose               uintptr
	GetInterfaceClass               uintptr
	GetInterfaceSubClass            uintptr
	GetInterfaceProtocol            uintptr
	GetDeviceVendor                 uintptr
	GetDeviceProduct                uintptr
	GetDeviceReleaseNumber          uintptr
	GetConfigurationValue           uintptr
	GetInterfaceNumber              uintptr
	GetAlternateSetting             uintptr
	GetNumEndpoints                 uintptr
	GetLocationID                   uintptr
	GetDevice                       uintptr
	SetAlternateInterface           uintptr
	GetBusFrameNumber               uintptr
	ControlRequest                  uintptr
	ControlRequestAsync             uintptr
	GetPipeProperties               uintptr
	GetPipeStatus                   uintptr
	AbortPipe                       uintptr
	ResetPipe                       uintptr
	ClearPipeStall                  uintptr
	ReadPipe                        uintptr
	WritePipe                       uintptr
	ReadPipeAsync                   uintptr
	WritePipeAsync                  uintptr
	ReadIsochPipeAsync              uintptr
	WriteIsochPipeAsync             uintptr
	ControlRequestTO                uintptr
	ControlRequestAsyncTO           uintptr
	ReadPipeTO                      uintptr
	WritePipeTO                     uintptr
	ReadPipeAsyncTO                 uintptr
	WritePipeAsyncTO                uintptr
	USBInterfaceGetStringIndex      uintptr
	USBInterfaceOpenSeize           uintptr
	ClearPipeStallBothEnds          uintptr
	SetPipePolicy                   uintptr
	GetBandwidthAvailable           uintptr
	GetEndpointProperties           uintptr
	LowLatencyReadIsochPipeAsync    uintptr
	LowLatencyWriteIsochPipeAsync   uintptr
	LowLatencyCreateBuffer          uintptr
	LowLatencyDestroyBuffer         uintptr
	GetBusMicroFrameNumber          uintptr
	GetFrameListTime                uintptr
	GetIOUSBLibVersion              uintptr
	FindNextAssociatedDescriptor    uintptr
	FindNextAltInterface            uintptr
	GetBusFrameNumberWithTime       uintptr
}

// kIOUSBFindInterfaceDontCare, from IOKit/usb/USB.h, matches any value of the
// IOUSBFindInterfaceRequest field it is used for.
const kIOUSBFindInterfaceDontCare = 0xFFFF

// ioUSBFindInterfaceRequest mirrors IOUSBFindInterfaceRequest from
// IOKit/usb/USB.h, the request CreateInterfaceIterator takes:
//
//	typedef struct {
//	    UInt16 bInterfaceClass;
//	    UInt16 bInterfaceSubClass;
//	    UInt16 bInterfaceProtocol;
//	    UInt16 bAlternateSetting;
//	} IOUSBFindInterfaceRequest;
//
// All four fields default to kIOUSBFindInterfaceDontCare, which returns every
// interface (at every alternate setting) as a separate io_service_t; the
// interface number itself is not a field here, so matching a specific
// interface means reading the bInterfaceNumber registry property off each
// service CreateInterfaceIterator returns.
type ioUSBFindInterfaceRequest struct {
	bInterfaceClass    uint16
	bInterfaceSubClass uint16
	bInterfaceProtocol uint16
	bAlternateSetting  uint16
}

// ioUSBIsocFrame mirrors IOUSBIsocFrame from IOKit/usb/USB.h, one element of
// the array ReadIsochPipeAsync/WriteIsochPipeAsync take and fill in per frame:
//
//	typedef struct IOUSBIsocFrame
//	{
//	    IOReturn frStatus;
//	    UInt16   frReqCount;
//	    UInt16   frActCount;
//	} IOUSBIsocFrame;
//
// IOReturn is a plain SInt32, so the three fields total 8 bytes with no
// padding.
type ioUSBIsocFrame struct {
	frStatus   int32
	frReqCount uint16
	frActCount uint16
}

// --- IOUSBDevRequest, the control transfer descriptor ----------------------

// ioUSBDevRequest, the control transfer descriptor IOKit's DeviceRequest takes.
//
//	typedef struct {
//	    UInt8   bmRequestType;
//	    UInt8   bRequest;
//	    UInt16  wValue;
//	    UInt16  wIndex;
//	    UInt16  wLength;
//	    void   *pData;
//	    UInt32  wLenDone;
//	} IOUSBDevRequest;
//
// This one is not packed: the six leading bytes fill the first eight, pData is
// pointer-aligned at offset 8, and wLenDone follows at 16 with the structure
// padded to 24. Go's natural layout matches, which the tests assert rather than
// assume — the same discipline that caught the CFUUIDBytes byte-order bug
// above.
type ioUSBDevRequest struct {
	bmRequestType uint8
	bRequest      uint8
	wValue        uint16
	wIndex        uint16
	wLength       uint16
	pData         uintptr
	wLenDone      uint32
	_             uint32 // tail padding, explicit so the size is unambiguous
}

// --- locationID handling -----------------------------------------------

// A locationID is a 32-bit value the USB host controller assigns. The high byte
// identifies the controller, and each following nibble is a port number one
// level further down the tree, zero-padded on the right:
//
//	0x14200000  ->  controller 0x14, port 2, then port 2 of that hub
//
// It is the closest thing macOS has to a bus number plus port path.

// busFromLocationID derives a bus number from a locationID.
//
// The high byte is the controller, which is the nearest equivalent of the bus
// number Linux reports. It is not a small counting number: real values look
// like 0x14 or 0xfa.
func busFromLocationID(locationID uint32) uint8 {
	return uint8(locationID >> 24)
}

// portChainFromLocationID returns the port numbers from the controller down to
// the device, which is the path a device occupies on the tree.
func portChainFromLocationID(locationID uint32) []uint8 {
	var chain []uint8
	// Walk the six nibbles below the controller byte, stopping at the first
	// zero: a zero nibble means the chain has ended.
	for shift := 20; shift >= 0; shift -= 4 {
		nibble := uint8((locationID >> uint(shift)) & 0xf)
		if nibble == 0 {
			break
		}
		chain = append(chain, nibble)
	}
	return chain
}

// iokitDevicePath formats the Path value for a macOS device, which
// IsValidDevicePath in turn parses.
func iokitDevicePath(locationID uint32) string {
	return fmt.Sprintf("iokit:%08x", locationID)
}
