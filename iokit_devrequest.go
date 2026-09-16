package usb

// IOUSBDevRequest, the control transfer descriptor IOKit's DeviceRequest takes.
//
// Untagged so the layout is offset-asserted on every host.
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
// assume — the same discipline that caught the CFUUIDBytes byte-order bug.
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
