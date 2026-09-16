package usb

import "encoding/binary"

// IOKit COM-style plug-in interface definitions.
//
// Untagged so the UUID values and the structure layouts are compiled and
// unit-tested on every host, not only on macOS. These are the values where a
// mistake does not produce an error: calling through a wrong vtable offset, or
// querying a wrong interface UUID, crashes the process. The tests below pin
// them.

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
// IOUSBLib.h, which are in network order: byte 0 is the most significant.
func uuidBytes(b [16]byte) cfUUIDBytes {
	return cfUUIDBytes{
		Lo: binary.BigEndian.Uint64(b[0:8]),
		Hi: binary.BigEndian.Uint64(b[8:16]),
	}
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
