package usb

import (
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// HID transport.
//
// A device claimed by the HID class driver cannot be opened with WinUSB, but
// hid.dll reaches it through the same kernel stack the class driver owns. This
// gives report-level access: input and output reports in place of interrupt
// transfers, and feature reports in place of HID class control requests. It
// exists for the large amount of test equipment that presents as a standard HID
// device to avoid shipping a driver.
//
// It cannot carry vendor-specific control transfers, bulk or isochronous
// traffic; hidclass.sys does not pass those. Those operations report
// ErrNotSupported.

var (
	modhid = windows.NewLazySystemDLL("hid.dll")

	procHidD_GetHidGuid        = modhid.NewProc("HidD_GetHidGuid")
	procHidD_GetAttributes     = modhid.NewProc("HidD_GetAttributes")
	procHidD_GetPreparsedData  = modhid.NewProc("HidD_GetPreparsedData")
	procHidD_FreePreparsedData = modhid.NewProc("HidD_FreePreparsedData")
	procHidP_GetCaps           = modhid.NewProc("HidP_GetCaps")
	procHidD_GetFeature        = modhid.NewProc("HidD_GetFeature")
	procHidD_SetFeature        = modhid.NewProc("HidD_SetFeature")
	procHidD_GetInputReport    = modhid.NewProc("HidD_GetInputReport")
	procHidD_SetOutputReport   = modhid.NewProc("HidD_SetOutputReport")
	procHidD_FlushQueue        = modhid.NewProc("HidD_FlushQueue")
)

// hiddAttributes is HIDD_ATTRIBUTES.
type hiddAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

// hidpCaps is HIDP_CAPS. Only the leading fields are used, but the whole
// structure must be present so hid.dll writes within our buffer.
type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

// hidGUID returns GUID_DEVINTERFACE_HID, asked of the system rather than
// hardcoded.
func hidGUID() windows.GUID {
	var guid windows.GUID
	syscall.SyscallN(procHidD_GetHidGuid.Addr(), uintptr(unsafe.Pointer(&guid)))
	return guid
}

// hidCollection is one top-level HID collection, which Windows exposes as its
// own device path even when several belong to the same USB interface.
type hidCollection struct {
	path       string
	handle     windows.Handle
	usagePage  uint16
	usage      uint16
	inputLen   int
	outputLen  int
	featureLen int
	iface      uint8
	collection int
}

// hidDevice is the HID transport for one USB device.
type hidDevice struct {
	mu          sync.Mutex
	collections []*hidCollection
	inputs      map[uint8]*hidCollection // interrupt IN endpoint -> collection
	outputs     map[uint8]*hidCollection // interrupt OUT endpoint -> collection
	closed      bool
}

// openHIDCollection opens one HID collection and reads its capabilities.
func openHIDCollection(path string) (*hidCollection, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	// Read/write is required for report I/O. Input devices are filtered out
	// before we get here, so a failure is a genuine problem rather than the
	// keyboard and mouse restriction.
	handle, err := windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}

	caps, err := hidCapabilities(handle)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}

	return &hidCollection{
		path:       path,
		handle:     handle,
		usagePage:  caps.UsagePage,
		usage:      caps.Usage,
		inputLen:   int(caps.InputReportByteLength),
		outputLen:  int(caps.OutputReportByteLength),
		featureLen: int(caps.FeatureReportByteLength),
		iface:      hidPathInterfaceNumber(path),
		collection: hidPathCollection(path),
	}, nil
}

// hidCapabilities reads HIDP_CAPS for an open collection.
func hidCapabilities(handle windows.Handle) (hidpCaps, error) {
	var preparsed uintptr
	r0, _, err := syscall.SyscallN(procHidD_GetPreparsedData.Addr(),
		uintptr(handle), uintptr(unsafe.Pointer(&preparsed)))
	if r0 == 0 {
		return hidpCaps{}, err
	}
	defer syscall.SyscallN(procHidD_FreePreparsedData.Addr(), preparsed)

	var caps hidpCaps
	r0, _, err = syscall.SyscallN(procHidP_GetCaps.Addr(),
		preparsed, uintptr(unsafe.Pointer(&caps)))
	if r0 == 0 {
		return hidpCaps{}, err
	}
	return caps, nil
}

// hidPeekUsable opens a collection only long enough to decide whether the
// selection policy allows it, so that input devices are never held open.
func hidPeekUsable(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}

	// Zero access is enough for HidP_GetCaps and is granted even for devices
	// that refuse read and write, so the policy can be applied without
	// requesting rights we should not have on a keyboard.
	handle, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	caps, err := hidCapabilities(handle)
	if err != nil {
		return false
	}
	return hidCollectionUsable(caps.UsagePage, caps.Usage)
}

// openHIDDevice builds a HID transport for a device, mapping its interrupt
// endpoints onto HID collections.
func openHIDDevice(dev *Device) (*hidDevice, error) {
	guid := hidGUID()
	candidates, err := enumerateWithGUID(&guid)
	if err != nil {
		return nil, err
	}

	h := &hidDevice{
		inputs:  make(map[uint8]*hidCollection),
		outputs: make(map[uint8]*hidCollection),
	}

	var excluded int
	for _, c := range candidates {
		// Only collections belonging to this device, established by walking up
		// the devnode tree rather than by matching path text.
		if !devnodeHasAncestor(c.DevInst, dev.devInst) {
			continue
		}
		if !hidPeekUsable(c.DevicePath) {
			excluded++
			continue
		}

		coll, err := openHIDCollection(c.DevicePath)
		if err != nil {
			continue
		}
		h.collections = append(h.collections, coll)
	}

	if len(h.collections) == 0 {
		if excluded > 0 {
			// The device is a HID device, but everything it exposes is a
			// pointing device or keyboard, which this library does not open.
			// Say so rather than letting the caller see a bare access error and
			// go looking for a permissions problem.
			return nil, ErrNotSupported
		}
		return nil, ErrNotFound
	}

	h.mapEndpoints(dev)
	return h, nil
}

// mapEndpoints associates the device's interrupt endpoints with the HID
// collection that serves them, so that InterruptTransfer works with the
// endpoint addresses the configuration descriptor advertises.
//
// Windows may split one USB interface into several collections. They share the
// interface's endpoints, so the first usable collection for an interface wins.
func (h *hidDevice) mapEndpoints(dev *Device) {
	if len(dev.rawConfigs) == 0 {
		return
	}

	var config ConfigDescriptor
	if err := config.Unmarshal(dev.rawConfigs[0]); err != nil {
		return
	}

	for _, iface := range config.Interfaces {
		for _, alt := range iface.AltSettings {
			coll := h.collectionForInterface(alt.InterfaceNumber)
			if coll == nil {
				continue
			}
			for _, ep := range alt.Endpoints {
				if ep.Attributes&0x03 != 0x03 { // interrupt endpoints only
					continue
				}
				if ep.EndpointAddr&0x80 != 0 {
					if _, seen := h.inputs[ep.EndpointAddr]; !seen {
						h.inputs[ep.EndpointAddr] = coll
					}
				} else {
					if _, seen := h.outputs[ep.EndpointAddr]; !seen {
						h.outputs[ep.EndpointAddr] = coll
					}
				}
			}
		}
	}
}

// collectionForInterface returns the lowest-numbered collection belonging to a
// USB interface.
func (h *hidDevice) collectionForInterface(iface uint8) *hidCollection {
	var best *hidCollection
	for _, c := range h.collections {
		if c.iface != iface {
			continue
		}
		if best == nil || c.collection < best.collection {
			best = c
		}
	}
	return best
}

// defaultCollection is used when an endpoint cannot be resolved, which happens
// if the configuration descriptor was unavailable.
func (h *hidDevice) defaultCollection() *hidCollection {
	if len(h.collections) == 0 {
		return nil
	}
	return h.collections[0]
}

func (h *hidDevice) close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil
	}
	h.closed = true

	for _, c := range h.collections {
		if c.handle != windows.InvalidHandle {
			windows.CloseHandle(c.handle)
			c.handle = windows.InvalidHandle
		}
	}
	return nil
}

// interruptTransfer reads or writes one report, chosen by endpoint direction.
func (h *hidDevice) interruptTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0, ErrDeviceNotFound
	}
	h.mu.Unlock()

	if endpoint&0x80 != 0 {
		coll := h.inputs[endpoint]
		if coll == nil {
			coll = h.defaultCollection()
		}
		if coll == nil {
			return 0, ErrNotFound
		}
		return coll.readReport(data, timeout)
	}

	coll := h.outputs[endpoint]
	if coll == nil {
		coll = h.defaultCollection()
	}
	if coll == nil {
		return 0, ErrNotFound
	}
	return coll.writeReport(data, timeout)
}

// readReport reads one input report, waiting up to timeout.
func (c *hidCollection) readReport(into []byte, timeout time.Duration) (int, error) {
	if c.inputLen == 0 {
		return 0, ErrNotSupported
	}

	report := make([]byte, c.inputLen)
	n, err := c.overlappedIO(report, timeout, false)
	if err != nil {
		return 0, err
	}
	return hidTrimInputReport(report[:n], into), nil
}

// writeReport writes one output report.
func (c *hidCollection) writeReport(payload []byte, timeout time.Duration) (int, error) {
	buf, err := hidReportBuffer(payload, c.outputLen)
	if err != nil {
		return 0, err
	}

	if _, err := c.overlappedIO(buf, timeout, true); err != nil {
		return 0, err
	}
	// Report the caller's payload as written, not the padding.
	return len(payload), nil
}

// overlappedIO performs one overlapped ReadFile or WriteFile with a timeout,
// cancelling and draining the request if it does not finish in time.
func (c *hidCollection) overlappedIO(buf []byte, timeout time.Duration, write bool) (int, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)

	overlapped := windows.Overlapped{HEvent: event}
	var done uint32

	if write {
		err = windows.WriteFile(c.handle, buf, &done, &overlapped)
	} else {
		err = windows.ReadFile(c.handle, buf, &done, &overlapped)
	}

	if err != nil && err != windows.ERROR_IO_PENDING {
		return 0, err
	}

	if err == windows.ERROR_IO_PENDING {
		timeoutMs := uint32(windows.INFINITE)
		if timeout > 0 {
			timeoutMs = uint32(timeout.Milliseconds())
		}

		wait, werr := windows.WaitForSingleObject(event, timeoutMs)
		if werr != nil {
			return 0, werr
		}
		if wait == uint32(windows.WAIT_TIMEOUT) {
			windows.CancelIoEx(c.handle, &overlapped)
			// Drain the cancelled request so the kernel is finished with the
			// buffer and the OVERLAPPED before they are reused.
			windows.GetOverlappedResult(c.handle, &overlapped, &done, true)
			return 0, ErrTimeout
		}
		if wait != uint32(windows.WAIT_OBJECT_0) {
			return 0, ErrIO
		}
		if err := windows.GetOverlappedResult(c.handle, &overlapped, &done, false); err != nil {
			return 0, err
		}
	}

	return int(done), nil
}

// getFeature reads a feature report. reportID is placed in the first byte, as
// hid.dll requires.
func (h *hidDevice) getFeature(reportID uint8, data []byte) (int, error) {
	coll := h.defaultCollection()
	if coll == nil {
		return 0, ErrNotFound
	}
	if coll.featureLen == 0 {
		return 0, ErrNotSupported
	}

	buf := make([]byte, coll.featureLen)
	buf[0] = reportID

	r0, _, err := syscall.SyscallN(procHidD_GetFeature.Addr(),
		uintptr(coll.handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r0 == 0 {
		return 0, err
	}
	return copy(data, buf), nil
}

// setFeature writes a feature report.
func (h *hidDevice) setFeature(reportID uint8, data []byte) error {
	coll := h.defaultCollection()
	if coll == nil {
		return ErrNotFound
	}

	buf, err := hidReportBuffer(append([]byte{reportID}, data...), coll.featureLen)
	if err != nil {
		return err
	}

	r0, _, e := syscall.SyscallN(procHidD_SetFeature.Addr(),
		uintptr(coll.handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r0 == 0 {
		return e
	}
	return nil
}

// getInputReport reads an input report on demand rather than waiting for the
// device to send one.
func (h *hidDevice) getInputReport(reportID uint8, data []byte) (int, error) {
	coll := h.defaultCollection()
	if coll == nil {
		return 0, ErrNotFound
	}
	if coll.inputLen == 0 {
		return 0, ErrNotSupported
	}

	buf := make([]byte, coll.inputLen)
	buf[0] = reportID

	r0, _, err := syscall.SyscallN(procHidD_GetInputReport.Addr(),
		uintptr(coll.handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r0 == 0 {
		return 0, err
	}
	return copy(data, buf), nil
}

// setOutputReport writes an output report through the control channel rather
// than the interrupt endpoint.
func (h *hidDevice) setOutputReport(reportID uint8, data []byte) error {
	coll := h.defaultCollection()
	if coll == nil {
		return ErrNotFound
	}

	buf, err := hidReportBuffer(append([]byte{reportID}, data...), coll.outputLen)
	if err != nil {
		return err
	}

	r0, _, e := syscall.SyscallN(procHidD_SetOutputReport.Addr(),
		uintptr(coll.handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r0 == 0 {
		return e
	}
	return nil
}

// flush discards any input reports the driver has queued.
func (h *hidDevice) flush() error {
	coll := h.defaultCollection()
	if coll == nil {
		return ErrNotFound
	}
	syscall.SyscallN(procHidD_FlushQueue.Addr(), uintptr(coll.handle))
	return nil
}
