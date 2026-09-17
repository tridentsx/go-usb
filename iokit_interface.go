package usb

// IOUSBInterfaceInterface300 method table, and the request structure used to
// find one.
//
// Untagged so the layout is compiled and offset-asserted on every host, for the
// same reason as ioUSBDeviceInterface in iokit_device_interface.go: a wrong
// offset here calls the wrong function, which crashes rather than returning an
// error.
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
