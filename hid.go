package usb

import (
	"strconv"
	"strings"
)

// HID logic that does not depend on any platform API, kept untagged so it is
// compiled and unit-tested on every host.
//
// The library talks to HID devices because a great deal of test equipment
// presents itself as a standard HID device to avoid shipping a driver: some
// report measurements directly in input reports, others tunnel a serial
// protocol over reports. Those are the devices this codepath exists for.
//
// Keyboards, mice and other pointing devices are deliberately excluded. Windows
// refuses read or write access to them anyway, but more importantly a USB
// library is the wrong place to read keystrokes from.

// HID usage pages and usages, from the HID Usage Tables.
const (
	hidUsagePageGenericDesktop = 0x01
	hidUsagePageDigitizer      = 0x0D

	hidUsagePointer  = 0x01
	hidUsageMouse    = 0x02
	hidUsageKeyboard = 0x06
	hidUsageKeypad   = 0x07
)

// hidCollectionUsable reports whether a HID collection is one this library will
// expose, given its usage page and usage.
//
// Everything is allowed except pointing devices and keyboards. Consumer control
// (page 0x0c) is deliberately allowed: monitors and instruments use it as a
// control channel, and it is not an input device in the sense that matters here.
func hidCollectionUsable(usagePage, usage uint16) bool {
	switch usagePage {
	case hidUsagePageGenericDesktop:
		switch usage {
		case hidUsagePointer, hidUsageMouse, hidUsageKeyboard, hidUsageKeypad:
			return false
		}
	case hidUsagePageDigitizer:
		// Touchpads and touchscreens.
		return false
	}
	return true
}

// hidPathInterfaceNumber extracts the USB interface number a HID collection
// belongs to, from the "&MI_xx" element Windows puts in the device path.
//
// A composite device's HID function on USB interface 2 appears as
//
//	\\?\HID#VID_413C&PID_4503&MI_02&Col02#8&2a20c0c&0&0001#{4d1e55b2-...}
//
// A device whose only function is HID has no MI_ element, and its collections
// belong to interface 0.
func hidPathInterfaceNumber(path string) uint8 {
	if n, ok := hidPathNumberAfter(path, "&mi_"); ok {
		return uint8(n)
	}
	return 0
}

// hidPathCollection extracts the one-based collection index from a HID device
// path, or 1 when the path names no collection.
func hidPathCollection(path string) int {
	if n, ok := hidPathNumberAfter(path, "&col"); ok {
		return n
	}
	return 1
}

// hidPathNumberAfter reads the decimal number following a marker in a device
// path. Markers are matched case-insensitively, since Windows is inconsistent
// about the case of these strings.
func hidPathNumberAfter(path, marker string) (int, bool) {
	lower := strings.ToLower(path)
	i := strings.Index(lower, marker)
	if i < 0 {
		return 0, false
	}

	rest := lower[i+len(marker):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}

	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// hidReportBuffer prepares an output report buffer of the exact length the
// device expects.
//
// Windows requires a write to be exactly OutputReportByteLength bytes, with the
// report ID first. Callers working at the report level already supply that
// leading byte, so the payload is copied as-is and the remainder zero-padded. A
// payload longer than the report is rejected rather than silently truncated,
// since for a device tunnelling a serial protocol a truncated report is
// corruption.
func hidReportBuffer(payload []byte, reportLength int) ([]byte, error) {
	if reportLength <= 0 {
		return nil, ErrNotSupported
	}
	if len(payload) > reportLength {
		return nil, ErrOverflow
	}

	buf := make([]byte, reportLength)
	copy(buf, payload)
	return buf, nil
}

// hidTrimInputReport copies a report that has been read into the caller's
// buffer, returning how many bytes were delivered.
//
// The report is passed through unmodified, including its leading report ID,
// which is what hidraw does on Linux and what the Windows HID stack produces.
// Callers therefore see the same bytes on every platform.
func hidTrimInputReport(report, into []byte) int {
	n := copy(into, report)
	return n
}
