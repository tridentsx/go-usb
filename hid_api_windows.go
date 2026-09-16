package usb

// Public HID surface for the Windows backend.
//
// These methods are meaningful when a device is reached through the HID class
// driver rather than WinUSB, which the library falls back to automatically. On a
// WinUSB device they report ErrNotSupported, since a WinUSB device has no HID
// report model.

// HID class request codes, from the HID specification.
const (
	hidRequestGetReport   = 0x01
	hidRequestGetIdle     = 0x02
	hidRequestGetProtocol = 0x03
	hidRequestSetReport   = 0x09
	hidRequestSetIdle     = 0x0A
	hidRequestSetProtocol = 0x0B
)

// HID report types, as they appear in the high byte of wValue.
const (
	hidReportTypeInput   = 0x01
	hidReportTypeOutput  = 0x02
	hidReportTypeFeature = 0x03
)

// IsHID reports whether this handle talks to the device through the HID class
// driver rather than WinUSB.
//
// A HID device supports report I/O and HID class requests, but not
// vendor-specific control transfers, bulk transfers or isochronous transfers.
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
