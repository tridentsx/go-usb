package usb

// Public HID surface for the Windows backend.
//
// These methods are meaningful when a device is reached through the HID class
// driver rather than WinUSB, which the library falls back to automatically. On a
// WinUSB device they report ErrNotSupported, since a WinUSB device has no HID
// report model.

// IsHID reports whether this handle talks to the device through the HID class
// driver rather than WinUSB.
//
// A HID device supports report I/O and HID class requests, but not
// vendor-specific control transfers, bulk transfers or isochronous transfers.
//
// This is a whole-handle decision, made exactly once in Device.Open based on
// whether WinUsb_Initialize succeeds for the file handle as a whole -- it is
// not, and cannot currently be, a per-interface one. ClaimInterface for a
// non-zero interface only calls WinUsb_GetAssociatedInterface (plain WinUSB
// pipe access) and never touches this decision. So for a composite device
// with one WinUSB interface and one HID interface, bound to WinUSB as a
// whole (e.g. via Zadig, which binds the entire device, not one interface),
// Open's WinUsb_Initialize succeeds and IsHID() reports false for the whole
// handle -- claiming the HID interface afterward changes nothing. This is
// unlike macOS, where IOKit exposes each interface as its own service, so
// per-interface HID attachment is architectural there (see
// devicehandle_darwin.go/hid_darwin.go) with no Windows equivalent yet.
//
// Closing this gap on Windows would mean adding a WinUSB-based HID
// implementation for a specific claimed interface -- technically
// straightforward, since GET_REPORT/SET_REPORT are just class-specific
// control transfers and Input reports are just interrupt-IN payloads, both
// already reachable once an interface is claimed -- but it requires the
// whole device to be WinUSB-bound already (as any Zadig-bound composite
// device already is), which makes the device invisible to every other
// application and to Windows' own input stack. Not implemented until a real
// consumer needs a composite WinUSB+HID device to work this way; see
// github.com/tridentsx/go-usb-jig's hid_windows_test.go for the real
// hardware case this was found against.
func (h *DeviceHandle) IsHID() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.hid != nil
}

// GetFeatureReport reads a HID feature report.
//
// Feature reports are how most HID test equipment exposes configuration and
// readings that are polled rather than pushed.
func (h *DeviceHandle) GetFeatureReport(reportID uint8, data []byte) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}
	if h.hid == nil {
		return 0, ErrNotSupported
	}
	return h.hid.getFeature(reportID, data)
}

// SetFeatureReport writes a HID feature report.
func (h *DeviceHandle) SetFeatureReport(reportID uint8, data []byte) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return ErrDeviceNotFound
	}
	if h.hid == nil {
		return ErrNotSupported
	}
	return h.hid.setFeature(reportID, data)
}

// GetInputReport polls an input report instead of waiting for the device to
// send one on its interrupt endpoint.
func (h *DeviceHandle) GetInputReport(reportID uint8, data []byte) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}
	if h.hid == nil {
		return 0, ErrNotSupported
	}
	return h.hid.getInputReport(reportID, data)
}

// SetOutputReport writes an output report through the control channel rather
// than an interrupt endpoint. Devices without an interrupt OUT endpoint, which
// is common, need this.
func (h *DeviceHandle) SetOutputReport(reportID uint8, data []byte) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return ErrDeviceNotFound
	}
	if h.hid == nil {
		return ErrNotSupported
	}
	return h.hid.setOutputReport(reportID, data)
}

// FlushHIDQueue discards input reports the driver has already queued.
//
// Useful before starting a request/response exchange with an instrument that
// streams unsolicited reports, so that stale data is not mistaken for a reply.
func (h *DeviceHandle) FlushHIDQueue() error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return ErrDeviceNotFound
	}
	if h.hid == nil {
		return ErrNotSupported
	}
	return h.hid.flush()
}

// HIDReportLengths reports the input, output and feature report sizes the
// device declares for the collection serving the given endpoint.
//
// Report sizes are fixed by the report descriptor, so a caller framing a
// protocol over reports needs them to size its buffers.
func (h *DeviceHandle) HIDReportLengths(endpoint uint8) (input, output, feature int, err error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, 0, 0, ErrDeviceNotFound
	}
	if h.hid == nil {
		return 0, 0, 0, ErrNotSupported
	}

	coll := h.hid.inputs[endpoint]
	if coll == nil {
		coll = h.hid.outputs[endpoint]
	}
	if coll == nil {
		coll = h.hid.defaultCollection()
	}
	if coll == nil {
		return 0, 0, 0, ErrNotFound
	}
	return coll.inputLen, coll.outputLen, coll.featureLen, nil
}

// hidControlTransfer serves the subset of control transfers that the HID class
// driver will carry, and reports ErrNotSupported for the rest.
//
// GET_REPORT and SET_REPORT map onto the hid.dll report calls. Anything
// vendor-specific cannot be expressed: hidclass.sys owns the control endpoint
// and will not pass arbitrary requests.
func (h *DeviceHandle) hidControlTransfer(requestType, request uint8, value, index uint16, data []byte) (int, error) {
	// Only class requests directed at an interface are candidates.
	const (
		typeMask  = 0x60
		typeClass = 0x20
	)
	if requestType&typeMask != typeClass {
		return 0, ErrNotSupported
	}

	reportType := uint8(value >> 8)
	reportID := uint8(value & 0xff)

	switch request {
	case hidRequestGetReport:
		switch reportType {
		case hidReportTypeFeature:
			return h.hid.getFeature(reportID, data)
		case hidReportTypeInput:
			return h.hid.getInputReport(reportID, data)
		}
	case hidRequestSetReport:
		switch reportType {
		case hidReportTypeFeature:
			return len(data), h.hid.setFeature(reportID, data)
		case hidReportTypeOutput:
			return len(data), h.hid.setOutputReport(reportID, data)
		}
	}

	return 0, ErrNotSupported
}
