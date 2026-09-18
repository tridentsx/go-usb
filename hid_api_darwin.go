package usb

// Public HID surface for the macOS backend, mirroring hid_api_windows.go.
//
// These methods are meaningful for an interface reached through the
// IOHIDDevice transport in hid_darwin.go rather than a claimed
// IOUSBInterfaceInterface, which ClaimInterface falls back to automatically
// when the interface is HID-class. On a non-HID interface they report
// ErrNotSupported, since it has no HID report model.

// IsHID reports whether this handle has a claimed interface reached through
// the HID transport rather than IOUSBInterfaceInterface.
//
// A HID interface supports report I/O and HID class requests, but not
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
	coll := h.hid.defaultCollection()
	if coll == nil {
		return 0, ErrNotFound
	}
	return coll.getFeature(reportID, data)
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
	coll := h.hid.defaultCollection()
	if coll == nil {
		return ErrNotFound
	}
	return coll.setFeature(reportID, data)
}

// GetInputReport polls an input report instead of waiting for the device to
// send one on its interrupt endpoint.
//
// See getInputReport's comment in hid_darwin.go: on-demand input polling is
// documented by Apple as having sporadic device support. InterruptTransfer,
// which uses the async pump instead, is the reliable way to receive input
// reports on this platform.
func (h *DeviceHandle) GetInputReport(reportID uint8, data []byte) (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return 0, ErrDeviceNotFound
	}
	if h.hid == nil {
		return 0, ErrNotSupported
	}
	coll := h.hid.defaultCollection()
	if coll == nil {
		return 0, ErrNotFound
	}
	return coll.getInputReport(reportID, data)
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
	coll := h.hid.defaultCollection()
	if coll == nil {
		return ErrNotFound
	}
	return coll.setOutputReport(reportID, data)
}

// FlushHIDQueue discards input reports the async pump has already queued.
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
	coll := h.hid.defaultCollection()
	if coll == nil {
		return ErrNotFound
	}
	return coll.flush()
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

// hidControlTransfer serves the subset of control transfers the HID class
// driver will carry, and reports ErrNotSupported for the rest, matching
// hid_api_windows.go's method of the same name. GET_REPORT and SET_REPORT map
// onto the IOHIDDevice report calls; anything vendor-specific cannot be
// expressed, since IOUSBHIDDriver owns the interface's control endpoint
// traffic here, not this process.
func (h *DeviceHandle) hidControlTransfer(requestType, request uint8, value, index uint16, data []byte) (int, error) {
	const (
		typeMask  = 0x60
		typeClass = 0x20
	)
	if requestType&typeMask != typeClass {
		return 0, ErrNotSupported
	}

	coll := h.hid.defaultCollection()
	if coll == nil {
		return 0, ErrNotFound
	}

	reportType := uint8(value >> 8)
	reportID := uint8(value & 0xff)

	switch request {
	case hidRequestGetReport:
		switch reportType {
		case hidReportTypeFeature:
			return coll.getFeature(reportID, data)
		case hidReportTypeInput:
			return coll.getInputReport(reportID, data)
		}
	case hidRequestSetReport:
		switch reportType {
		case hidReportTypeFeature:
			return len(data), coll.setFeature(reportID, data)
		case hidReportTypeOutput:
			return len(data), coll.setOutputReport(reportID, data)
		}
	}

	return 0, ErrNotSupported
}
