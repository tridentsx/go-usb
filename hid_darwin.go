// HID transport for macOS, via IOKit's IOHIDDevice API rather than
// IOUSBInterfaceInterface.
//
// A HID-class USB interface is not claimable the normal way: IOUSBHIDDriver
// (a kernel driver, itself an IOHIDDevice) already has it open exclusively,
// so ClaimInterface's USBInterfaceOpen fails with kIOReturnExclusiveAccess --
// the same signal WinUsb_Initialize failing is on Windows. Unlike Windows,
// though, IOUSBHIDDriver's IOHIDDevice instance is reachable directly: it is
// the interface's own child in the IOService registry plane, found with
// IOObjectConformsTo rather than by guessing at a IOHIDManager device-matching
// correlation. Confirmed against hidapi's real macOS backend (mac/hid.c),
// which does the equivalent walk from a known registry entry rather than
// enumerating IOHIDManagerCopyDevices and matching on locationID.
//
// This gives report-level access, the same capability hid_windows.go's
// hid.dll transport provides: input reports (delivered asynchronously,
// mirroring an interrupt IN endpoint), output and feature reports, and HID
// class GET_REPORT/SET_REPORT. It cannot carry vendor-specific control
// transfers, bulk or isochronous traffic -- IOUSBHIDDriver owns the interface
// and IOHIDDevice exposes no such calls.
//
// One IOHIDDeviceRef corresponds to one USB interface here, unlike Windows,
// which can split a single interface into several top-level-collection device
// paths. That means hidDevice below only ever holds one hidCollection: a
// composite device with more than one HID interface is not yet supported,
// the same limitation class as this library's existing "only the first
// WinUSB function is used on a composite device" note for Windows.

package usb

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// IOHIDReportType values, from the real IOHIDKeys.h enum
// (kIOHIDReportTypeInput = 0, kIOHIDReportTypeOutput, kIOHIDReportTypeFeature).
const (
	kIOHIDReportTypeInput   = 0
	kIOHIDReportTypeOutput  = 1
	kIOHIDReportTypeFeature = 2
)

// kIOHIDOptionsTypeNone is IOHIDDeviceOpen/Close's options argument when no
// special behavior (such as kIOHIDOptionsTypeSeizeDevice) is requested.
const kIOHIDOptionsTypeNone = 0

// HID property keys, from IOHIDKeys.h, exact spelling.
const (
	propHIDMaxInputReportSize   = "MaxInputReportSize"
	propHIDMaxOutputReportSize  = "MaxOutputReportSize"
	propHIDMaxFeatureReportSize = "MaxFeatureReportSize"
	propHIDPrimaryUsagePage     = "PrimaryUsagePage"
	propHIDPrimaryUsage         = "PrimaryUsage"
)

// hidFuncs holds the IOHIDDevice and registry-walk entry points, resolved
// once alongside the rest of loadIOKit's symbols.
type hidFuncs struct {
	IORegistryEntryGetChildIterator func(entry uint32, plane string, iterator *uint32) int32
	IOObjectConformsTo              func(object uint32, className string) bool

	IOHIDDeviceCreate                      func(allocator uintptr, service uint32) uintptr
	IOHIDDeviceOpen                        func(device uintptr, options uint32) int32
	IOHIDDeviceClose                       func(device uintptr, options uint32) int32
	IOHIDDeviceGetProperty                 func(device uintptr, key uintptr) uintptr
	IOHIDDeviceSetReport                   func(device uintptr, reportType int32, reportID int64, report *byte, length int64) int32
	IOHIDDeviceGetReport                   func(device uintptr, reportType int32, reportID int64, report *byte, length *int64) int32
	IOHIDDeviceRegisterInputReportCallback func(device uintptr, report *byte, length int64, callback uintptr, context uintptr)
	IOHIDDeviceScheduleWithRunLoop         func(device uintptr, runLoop uintptr, mode uintptr)
	IOHIDDeviceUnscheduleFromRunLoop       func(device uintptr, runLoop uintptr, mode uintptr)
}

var hid hidFuncs

// registerHIDFuncs resolves the HID entry points. Called from loadIOKit's
// once, so it shares its error handling.
func registerHIDFuncs(ioKit, cf uintptr) {
	purego.RegisterLibFunc(&hid.IORegistryEntryGetChildIterator, ioKit, "IORegistryEntryGetChildIterator")
	purego.RegisterLibFunc(&hid.IOObjectConformsTo, ioKit, "IOObjectConformsTo")

	purego.RegisterLibFunc(&hid.IOHIDDeviceCreate, ioKit, "IOHIDDeviceCreate")
	purego.RegisterLibFunc(&hid.IOHIDDeviceOpen, ioKit, "IOHIDDeviceOpen")
	purego.RegisterLibFunc(&hid.IOHIDDeviceClose, ioKit, "IOHIDDeviceClose")
	purego.RegisterLibFunc(&hid.IOHIDDeviceGetProperty, ioKit, "IOHIDDeviceGetProperty")
	purego.RegisterLibFunc(&hid.IOHIDDeviceSetReport, ioKit, "IOHIDDeviceSetReport")
	purego.RegisterLibFunc(&hid.IOHIDDeviceGetReport, ioKit, "IOHIDDeviceGetReport")
	purego.RegisterLibFunc(&hid.IOHIDDeviceRegisterInputReportCallback, ioKit, "IOHIDDeviceRegisterInputReportCallback")
	purego.RegisterLibFunc(&hid.IOHIDDeviceScheduleWithRunLoop, ioKit, "IOHIDDeviceScheduleWithRunLoop")
	purego.RegisterLibFunc(&hid.IOHIDDeviceUnscheduleFromRunLoop, ioKit, "IOHIDDeviceUnscheduleFromRunLoop")
}

// hidCollection is the one HID interface this transport talks to.
type hidCollection struct {
	device     uintptr // IOHIDDeviceRef
	inputLen   int
	outputLen  int
	featureLen int
	iface      uint8

	// reports queues input reports IOKit's asynchronous callback delivers,
	// read by interruptTransfer's IN direction and drained by flush. Each
	// report is prefixed with its report ID byte, matching hidraw on Linux
	// and hid.dll on Windows -- the callback hands report ID and data
	// separately, so startInputPump reassembles them before queuing.
	reports chan []byte

	pumpMu      sync.Mutex
	pumpStarted bool
	pumpStopped bool
	pumpErr     error
	runLoop     uintptr
	mode        uintptr
	pumpReady   chan struct{}
	pumpDone    chan struct{}
}

// hidDevice is the HID transport for one USB device, matching hid_windows.go's
// type of the same name so hid_api_darwin.go can mirror hid_api_windows.go
// almost exactly.
type hidDevice struct {
	collections []*hidCollection
	inputs      map[uint8]*hidCollection
	outputs     map[uint8]*hidCollection
}

// openHIDInterface opens the HID transport for USB interface iface, whose
// io_service_t (service) ClaimInterface just failed to open exclusively with
// kIOReturnExclusiveAccess. h is used to read the configuration descriptor for
// endpoint-to-collection mapping; it must not be locked by the caller beyond
// what ClaimInterface already holds (this reads h.devInterface directly, with
// no locking of its own, relying on ClaimInterface's own h.mu.Lock() for
// exclusion).
func openHIDInterface(h *DeviceHandle, iface uint8, service uint32) (*hidDevice, error) {
	k, err := loadIOKit()
	if err != nil {
		return nil, err
	}

	hidService, err := k.findHIDChild(service)
	if err != nil {
		return nil, err
	}
	defer k.IOObjectRelease(hidService)

	device := hid.IOHIDDeviceCreate(0, hidService)
	if device == 0 {
		return nil, fmt.Errorf("IOHIDDeviceCreate: %w", ErrIO)
	}

	// Applied before opening, matching hid_windows.go's hidPeekUsable: a
	// keyboard or pointing device is never a candidate this library will
	// serve, regardless of platform. See hidCollectionUsable in hid.go for
	// why (Windows also refuses read/write access to these; a USB library is
	// the wrong place to read keystrokes from either way).
	usagePage := k.hidPropertyInt(device, propHIDPrimaryUsagePage)
	usage := k.hidPropertyInt(device, propHIDPrimaryUsage)
	if !hidCollectionUsable(uint16(usagePage), uint16(usage)) {
		k.CFRelease(device)
		return nil, ErrNotSupported
	}

	if ret := hid.IOHIDDeviceOpen(device, kIOHIDOptionsTypeNone); int32(ret) != kernSuccess {
		k.CFRelease(device)
		return nil, fmt.Errorf("IOHIDDeviceOpen: IOReturn %#x: %w", uint32(ret), ErrIO)
	}

	coll := &hidCollection{
		device:     device,
		inputLen:   k.hidPropertyInt(device, propHIDMaxInputReportSize),
		outputLen:  k.hidPropertyInt(device, propHIDMaxOutputReportSize),
		featureLen: k.hidPropertyInt(device, propHIDMaxFeatureReportSize),
		iface:      iface,
		reports:    make(chan []byte, 32),
	}

	if err := coll.startInputPump(); err != nil {
		hid.IOHIDDeviceClose(device, kIOHIDOptionsTypeNone)
		k.CFRelease(device)
		return nil, err
	}

	config, err := fetchConfigDescriptor(h.devInterface, 0)

	d := &hidDevice{
		collections: []*hidCollection{coll},
		inputs:      make(map[uint8]*hidCollection),
		outputs:     make(map[uint8]*hidCollection),
	}
	if err == nil {
		d.mapEndpoints(config, iface, coll)
	}
	return d, nil
}

// findHIDChild finds service's child in the IOService plane that conforms to
// IOHIDDevice -- IOUSBHIDDriver, the kernel driver that opened the interface
// exclusively, is such a child. The caller releases the returned service.
func (k *iokitFuncs) findHIDChild(service uint32) (uint32, error) {
	var iterator uint32
	if ret := hid.IORegistryEntryGetChildIterator(service, "IOService\x00", &iterator); ret != kernSuccess {
		return 0, ErrDeviceNotFound
	}
	defer k.IOObjectRelease(iterator)

	for {
		child := k.IOIteratorNext(iterator)
		if child == 0 {
			break
		}
		if hid.IOObjectConformsTo(child, "IOHIDDevice\x00") {
			return child, nil
		}
		k.IOObjectRelease(child)
	}
	return 0, ErrDeviceNotFound
}

// hidPropertyInt reads an integer property directly off an IOHIDDeviceRef.
// Unlike propertyNumber, which looks a key up in a property dictionary
// snapshot, this calls IOHIDDeviceGetProperty, the live per-device accessor
// IOHIDDevice exposes instead. The returned CFTypeRef is a "Get", not a
// "Copy", so it is not released here.
func (k *iokitFuncs) hidPropertyInt(device uintptr, key string) int {
	cfKey := k.cfStringRef(key)
	if cfKey == 0 {
		return 0
	}
	defer k.CFRelease(cfKey)

	value := hid.IOHIDDeviceGetProperty(device, cfKey)
	if value == 0 || k.CFGetTypeID(value) != k.CFNumberGetTypeID() {
		return 0
	}
	var out int64
	if !k.CFNumberGetValue(value, kCFNumberSInt64Type, unsafe.Pointer(&out)) {
		return 0
	}
	return int(out)
}

// mapEndpoints associates the interface's interrupt endpoints with coll, so
// that InterruptTransfer works with the endpoint addresses the configuration
// descriptor advertises, matching hid_windows.go's mapEndpoints.
func (d *hidDevice) mapEndpoints(config *ConfigDescriptor, iface uint8, coll *hidCollection) {
	for _, i := range config.Interfaces {
		for _, alt := range i.AltSettings {
			if alt.InterfaceNumber != iface {
				continue
			}
			for _, ep := range alt.Endpoints {
				if ep.Attributes&0x03 != 0x03 { // interrupt endpoints only
					continue
				}
				if ep.EndpointAddr&0x80 != 0 {
					d.inputs[ep.EndpointAddr] = coll
				} else {
					d.outputs[ep.EndpointAddr] = coll
				}
			}
		}
	}
}

// defaultCollection is used when an endpoint cannot be resolved, or for the
// device-level report calls (GetFeatureReport etc.), which are not
// per-endpoint.
func (d *hidDevice) defaultCollection() *hidCollection {
	if len(d.collections) == 0 {
		return nil
	}
	return d.collections[0]
}

// ownsInterface reports whether iface is the USB interface this transport was
// opened for, so ReleaseInterface knows when to tear it down.
func (d *hidDevice) ownsInterface(iface uint8) bool {
	return len(d.collections) > 0 && d.collections[0].iface == iface
}

func (d *hidDevice) close() error {
	for _, c := range d.collections {
		c.stopInputPump()
		hid.IOHIDDeviceClose(c.device, kIOHIDOptionsTypeNone)
		if k, err := loadIOKit(); err == nil {
			k.CFRelease(c.device)
		}
	}
	d.collections = nil
	return nil
}

// interruptTransfer reads or writes one report, chosen by endpoint direction,
// matching hid_windows.go's method of the same name.
func (d *hidDevice) interruptTransfer(endpoint uint8, data []byte, timeout time.Duration) (int, error) {
	if endpoint&0x80 != 0 {
		coll := d.inputs[endpoint]
		if coll == nil {
			coll = d.defaultCollection()
		}
		if coll == nil {
			return 0, ErrNotFound
		}
		return coll.readReport(data, timeout)
	}

	coll := d.outputs[endpoint]
	if coll == nil {
		coll = d.defaultCollection()
	}
	if coll == nil {
		return 0, ErrNotFound
	}
	return coll.writeReport(data, timeout)
}

// readReport waits up to timeout for the next input report the async pump
// has queued. timeout <= 0 blocks indefinitely: the nil timer channel that
// leaves is simply never ready to receive.
func (c *hidCollection) readReport(into []byte, timeout time.Duration) (int, error) {
	if c.inputLen == 0 {
		return 0, ErrNotSupported
	}

	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case report, ok := <-c.reports:
		if !ok {
			return 0, ErrDeviceNotFound
		}
		return hidTrimInputReport(report, into), nil
	case <-timerC:
		return 0, ErrTimeout
	}
}

// writeReport writes one output report. payload's leading byte is the report
// ID, matching the portable convention hidraw and hid.dll both use;
// IOHIDDeviceSetReport wants it as a separate argument instead, so it is
// split back out here.
func (c *hidCollection) writeReport(payload []byte, timeout time.Duration) (int, error) {
	buf, err := hidReportBuffer(payload, c.outputLen)
	if err != nil {
		return 0, err
	}
	if len(buf) == 0 {
		return 0, ErrNotSupported
	}

	reportID := int64(buf[0])
	rest := buf[1:]
	var restPtr *byte
	if len(rest) > 0 {
		restPtr = &rest[0]
	}

	ret := hid.IOHIDDeviceSetReport(c.device, kIOHIDReportTypeOutput, reportID, restPtr, int64(len(rest)))
	if ret != kernSuccess {
		return 0, fmt.Errorf("IOHIDDeviceSetReport(output): IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return len(payload), nil
}

// getFeature reads a feature report. reportID is a separate IOHIDDevice
// parameter, unlike hid.dll's leading-byte convention, so data holds only the
// report's payload, not the ID.
func (c *hidCollection) getFeature(reportID uint8, data []byte) (int, error) {
	if c.featureLen == 0 {
		return 0, ErrNotSupported
	}

	buf := make([]byte, c.featureLen)
	length := int64(len(buf))
	ret := hid.IOHIDDeviceGetReport(c.device, kIOHIDReportTypeFeature, int64(reportID), &buf[0], &length)
	if ret != kernSuccess {
		return 0, fmt.Errorf("IOHIDDeviceGetReport(feature): IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	if length < 0 || int(length) > len(buf) {
		length = int64(len(buf))
	}
	return copy(data, buf[:length]), nil
}

// setFeature writes a feature report.
func (c *hidCollection) setFeature(reportID uint8, data []byte) error {
	buf, err := hidReportBuffer(data, c.featureLen)
	if err != nil {
		return err
	}
	var bufPtr *byte
	if len(buf) > 0 {
		bufPtr = &buf[0]
	}
	ret := hid.IOHIDDeviceSetReport(c.device, kIOHIDReportTypeFeature, int64(reportID), bufPtr, int64(len(buf)))
	if ret != kernSuccess {
		return fmt.Errorf("IOHIDDeviceSetReport(feature): IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return nil
}

// getInputReport polls an input report on demand rather than waiting for the
// async pump to receive one.
//
// Apple documents this call as intended for feature reports; on-demand input
// polling has "sporadic device support" (confirmed via Apple's own developer
// forums, not just this comment) -- callers wanting reliable input access
// should use InterruptTransfer, which goes through the async pump instead.
func (c *hidCollection) getInputReport(reportID uint8, data []byte) (int, error) {
	if c.inputLen == 0 {
		return 0, ErrNotSupported
	}

	buf := make([]byte, c.inputLen)
	length := int64(len(buf))
	ret := hid.IOHIDDeviceGetReport(c.device, kIOHIDReportTypeInput, int64(reportID), &buf[0], &length)
	if ret != kernSuccess {
		return 0, fmt.Errorf("IOHIDDeviceGetReport(input): IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	if length < 0 || int(length) > len(buf) {
		length = int64(len(buf))
	}
	return copy(data, buf[:length]), nil
}

// setOutputReport writes an output report through the control channel,
// matching hid_windows.go's HidD_SetOutputReport (as opposed to writeReport,
// which is used for an actual interrupt OUT endpoint). IOHIDDeviceSetReport
// itself is a single call for both cases; the distinction is which report
// type/endpoint the caller means, not a different underlying mechanism here.
func (c *hidCollection) setOutputReport(reportID uint8, data []byte) error {
	return c.setFeatureOrOutput(kIOHIDReportTypeOutput, reportID, data)
}

func (c *hidCollection) setFeatureOrOutput(reportType int32, reportID uint8, data []byte) error {
	buf, err := hidReportBuffer(data, c.outputLen)
	if err != nil {
		return err
	}
	var bufPtr *byte
	if len(buf) > 0 {
		bufPtr = &buf[0]
	}
	ret := hid.IOHIDDeviceSetReport(c.device, reportType, int64(reportID), bufPtr, int64(len(buf)))
	if ret != kernSuccess {
		return fmt.Errorf("IOHIDDeviceSetReport: IOReturn %#x: %w", uint32(ret), ErrIO)
	}
	return nil
}

// flush discards any input reports the pump has queued but nothing has read
// yet.
func (c *hidCollection) flush() error {
	for {
		select {
		case <-c.reports:
		default:
			return nil
		}
	}
}

// --- async input-report pump -------------------------------------------------
//
// Mirrors IOUSBInterfaceInterface's ensureAsyncPump/stopAsyncPump in
// isochronous_darwin.go: one goroutine locked to an OS thread, running a
// CFRunLoop that IOHIDDeviceScheduleWithRunLoop delivers input-report
// completions through.

// startInputPump starts the collection's async input-report pump.
func (c *hidCollection) startInputPump() error {
	c.pumpMu.Lock()
	if c.pumpStarted {
		err := c.pumpErr
		c.pumpMu.Unlock()
		return err
	}
	c.pumpStarted = true
	c.pumpMu.Unlock()

	k, err := loadIOKit()
	if err != nil {
		c.pumpMu.Lock()
		c.pumpErr = err
		c.pumpMu.Unlock()
		return err
	}

	mode := k.cfStringRef("kCFRunLoopDefaultMode")
	if mode == 0 {
		c.pumpMu.Lock()
		c.pumpErr = ErrOther
		c.pumpMu.Unlock()
		return ErrOther
	}

	reportBuf := make([]byte, c.inputLenOrDefault())
	var reportBufPtr *byte
	if len(reportBuf) > 0 {
		reportBufPtr = &reportBuf[0]
	}

	callback := purego.NewCallback(func(context uintptr, result int32, sender uintptr, reportType int32, reportID uint32, report unsafe.Pointer, reportLength int64) {
		if reportLength < 0 {
			return
		}
		buf := make([]byte, 1+int(reportLength))
		buf[0] = byte(reportID)
		if reportLength > 0 {
			copy(buf[1:], unsafe.Slice((*byte)(report), int(reportLength)))
		}
		select {
		case c.reports <- buf:
		default:
			// Queue is full: drop the oldest to make room for the newest,
			// matching FlushHIDQueue's "discard stale reports" intent rather
			// than losing the report that just arrived.
			select {
			case <-c.reports:
			default:
			}
			select {
			case c.reports <- buf:
			default:
			}
		}
	})

	c.pumpReady = make(chan struct{})
	c.pumpDone = make(chan struct{})
	c.mode = mode

	go func() {
		runtime.LockOSThread()
		// Deliberately never unlocked; see the identical note in
		// hotplug_darwin.go's pump goroutine.

		hid.IOHIDDeviceRegisterInputReportCallback(c.device, reportBufPtr, int64(len(reportBuf)), callback, 0)

		rl := hotplug.CFRunLoopGetCurrent()
		hid.IOHIDDeviceScheduleWithRunLoop(c.device, rl, mode)

		c.pumpMu.Lock()
		c.runLoop = rl
		c.pumpMu.Unlock()
		close(c.pumpReady)

		for {
			c.pumpMu.Lock()
			stopped := c.pumpStopped
			c.pumpMu.Unlock()
			if stopped {
				break
			}
			hotplug.CFRunLoopRunInMode(mode, 3600, true)
		}

		hid.IOHIDDeviceUnscheduleFromRunLoop(c.device, rl, mode)
		k.CFRelease(mode)
		close(c.pumpDone)
	}()

	return nil
}

// inputLenOrDefault is the buffer IOHIDDeviceRegisterInputReportCallback
// writes into. A device with no input reports at all still gets a small
// buffer, since the callback is harmless if it's simply never invoked.
func (c *hidCollection) inputLenOrDefault() int {
	if c.inputLen > 0 {
		return c.inputLen
	}
	return 64
}

// stopInputPump stops the pump started by startInputPump, if one was, and
// blocks until it has exited.
func (c *hidCollection) stopInputPump() {
	c.pumpMu.Lock()
	if !c.pumpStarted || c.pumpStopped {
		c.pumpMu.Unlock()
		return
	}
	c.pumpStopped = true
	ready := c.pumpReady
	c.pumpMu.Unlock()

	if ready == nil {
		return
	}
	<-ready
	c.pumpMu.Lock()
	rl := c.runLoop
	c.pumpMu.Unlock()
	if rl != 0 {
		hotplug.CFRunLoopStop(rl)
	}
	<-c.pumpDone
}
