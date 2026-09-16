package usb

// IOUSBDeviceInterface method table.
//
// Untagged so the layout is compiled and offset-asserted on every host. The
// order is the declaration order in IOKit/usb/IOUSBLib.h and it *is* the ABI: a
// wrong offset calls the wrong function, which crashes rather than returning an
// error, and the cause can be several calls upstream of the symptom.
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
