package usb

import "encoding/binary"

// This file holds byte-level USB wire encoding that is independent of any
// platform API. Keeping it untagged means it is compiled and unit-tested on
// every host, including when the platform backend that uses it cannot be built.

// setupPacketSize is the wire size of a USB control setup packet.
const setupPacketSize = 8

// encodeSetupPacket lays out a USB control setup packet exactly as USB 2.0
// Table 9-2 specifies: bmRequestType, bRequest, wValue, wIndex and wLength,
// with the 16-bit fields little-endian.
//
// Windows needs this as a value rather than a pointer, because
// WinUsb_ControlTransfer takes its WINUSB_SETUP_PACKET argument by value.
func encodeSetupPacket(requestType, request uint8, value, index, length uint16) [setupPacketSize]byte {
	var p [setupPacketSize]byte
	p[0] = requestType
	p[1] = request
	binary.LittleEndian.PutUint16(p[2:4], value)
	binary.LittleEndian.PutUint16(p[4:6], index)
	binary.LittleEndian.PutUint16(p[6:8], length)
	return p
}

// descriptorRequestValue builds the wValue of a GET_DESCRIPTOR or
// SET_DESCRIPTOR request from a descriptor type and index.
func descriptorRequestValue(descType, descIndex uint8) uint16 {
	return uint16(descType)<<8 | uint16(descIndex)
}

// decodeStringDescriptor converts a USB string descriptor into a Go string.
//
// A string descriptor is a length byte, a type byte, then UTF-16LE code units.
// Anything shorter than the declared length is tolerated, so a truncated read
// yields the part that did arrive rather than an error.
func decodeStringDescriptor(desc []byte) string {
	if len(desc) < 2 {
		return ""
	}

	length := int(desc[0])
	if length > len(desc) {
		length = len(desc)
	}
	if length < 2 {
		return ""
	}

	units := make([]uint16, 0, (length-2)/2)
	for i := 2; i+1 < length; i += 2 {
		units = append(units, binary.LittleEndian.Uint16(desc[i:i+2]))
	}
	return string(utf16Decode(units))
}

// utf16Decode converts UTF-16 code units to runes, stopping at a NUL and
// decoding surrogate pairs.
func utf16Decode(units []uint16) []rune {
	runes := make([]rune, 0, len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u == 0 {
			break
		}
		if u >= 0xD800 && u < 0xDC00 && i+1 < len(units) {
			low := units[i+1]
			if low >= 0xDC00 && low < 0xE000 {
				runes = append(runes, ((rune(u)-0xD800)<<10|(rune(low)-0xDC00))+0x10000)
				i++
				continue
			}
		}
		runes = append(runes, rune(u))
	}
	return runes
}
