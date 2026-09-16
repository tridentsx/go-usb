//go:build darwin && !cgo

// Hotplug notifications via IOKit, without cgo.
//
// IOServiceAddMatchingNotification arms a notification against a matching
// dictionary and delivers it through a CFRunLoop, so watching for hotplug
// events needs a run loop actually running somewhere — unlike enumeration or
// a synchronous transfer, this can't just make a call and return. Each
// registration gets its own goroutine that locks itself to one OS thread (a
// CFRunLoop belongs to the thread that pumps it, not to a goroutine, which the
// runtime is otherwise free to move between threads at any suspension point)
// and parks in CFRunLoopRunInMode for the registration's whole life.
//
// Only the "IOUSBHostDevice" class is matched, unlike enumeration's dual-class
// loop over IOUSBHostDevice and IOUSBDevice for compatibility with older
// systems: that compatibility concern matters less for a feature being added
// new today. Extending this to loop over both, the same way enumeration does,
// is a small increment if it turns out to matter.
//
// See issue #14 and HotplugCallback's doc comment for the concurrency
// contract this maintains.

package usb

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// IOKit notification-type strings, from IOKit/IOKitKeys.h. Arrival uses
// kIOFirstMatchNotification (fired once per IOService instance, after
// matching drivers have probed and started) rather than kIOMatchedNotification
// (which can refire for the same instance), matching Apple's own USB hotplug
// sample code.
const (
	kIOFirstMatchNotification = "IOServiceFirstMatch"
	kIOTerminatedNotification = "IOServiceTerminate"
)

// hotplugFuncs holds the IOKit notification-port and CoreFoundation run-loop
// entry points, resolved once alongside the rest of loadIOKit's symbols.
type hotplugFuncs struct {
	IONotificationPortCreate           func(mainPort uint32) uintptr
	IONotificationPortGetRunLoopSource func(notify uintptr) uintptr
	IONotificationPortDestroy          func(notify uintptr)

	// IOServiceAddMatchingNotification's notificationType parameter is
	// io_name_t, a fixed char array, which C parameter-passing rules decay to
	// a plain char* at the ABI level -- exactly like IOServiceMatching's
	// const char*, so it is declared as a Go string here for the same reason.
	// matching, callback and refCon are opaque C values with no Go-side
	// meaning of their own, so they stay as uintptr.
	IOServiceAddMatchingNotification func(notifyPort uintptr, notificationType string, matching uintptr, callback uintptr, refCon uintptr, notification *uint32) int32

	CFRunLoopGetCurrent func() uintptr
	CFRunLoopAddSource  func(rl, source, mode uintptr)
	CFRunLoopRunInMode  func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	CFRunLoopStop       func(rl uintptr)
}

var hotplug hotplugFuncs

// registerHotplugFuncs resolves the hotplug entry points. Called from
// loadIOKit's once, so it shares its error handling.
func registerHotplugFuncs(ioKit, cf uintptr) {
	purego.RegisterLibFunc(&hotplug.IONotificationPortCreate, ioKit, "IONotificationPortCreate")
	purego.RegisterLibFunc(&hotplug.IONotificationPortGetRunLoopSource, ioKit, "IONotificationPortGetRunLoopSource")
	purego.RegisterLibFunc(&hotplug.IONotificationPortDestroy, ioKit, "IONotificationPortDestroy")
	purego.RegisterLibFunc(&hotplug.IOServiceAddMatchingNotification, ioKit, "IOServiceAddMatchingNotification")

	purego.RegisterLibFunc(&hotplug.CFRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&hotplug.CFRunLoopAddSource, cf, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&hotplug.CFRunLoopRunInMode, cf, "CFRunLoopRunInMode")
	purego.RegisterLibFunc(&hotplug.CFRunLoopStop, cf, "CFRunLoopStop")
}

// darwinHotplugHandle is the HotplugHandle returned to the caller.
type darwinHotplugHandle struct {
	mu      sync.Mutex
	stopped bool
	runLoop uintptr // captured by the pump goroutine; valid once ready is closed

	ready chan struct{} // closed once runLoop is captured and the source is added
	done  chan struct{} // closed once the pump goroutine has cleaned up and exited
}

// Deregister stops the watch and blocks until its goroutine has fully
// released the notification port, so no callback fires after this returns.
func (h *darwinHotplugHandle) Deregister() error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	h.stopped = true
	h.mu.Unlock()

	<-h.ready
	if h.runLoop != 0 {
		hotplug.CFRunLoopStop(h.runLoop)
	}
	<-h.done
	return nil
}

// registerHotplugCallback implements RegisterHotplugCallback for the CGO-free
// macOS backend.
//
// Every callback invocation, including the reports for devices that are
// already connected at registration time -- kIOFirstMatchNotification's
// initial iterator drain, which arms the notification, includes those the
// same way it includes anything that matches afterward -- happens from the
// registration's own goroutine, never from the caller's.
func registerHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	k, err := loadIOKit()
	if err != nil {
		return nil, err
	}
	if hotplug.IONotificationPortCreate == nil {
		return nil, ErrNotSupported
	}

	// kCFRunLoopDefaultMode's value is documented as literally the string
	// "kCFRunLoopDefaultMode" (CFRunLoop.h), and CFRunLoop matches modes by
	// string equality (CFEqual), not by object identity, so building an equal
	// CFString here works exactly like using Apple's own constant would --
	// without reading a foreign global variable's value through a dlsym'd
	// address, which is the unsafe.Pointer(uintptr) conversion go vet's
	// unsafeptr check exists to catch.
	mode := k.cfStringRef("kCFRunLoopDefaultMode")
	if mode == 0 {
		return nil, ErrOther
	}

	notifyPort := hotplug.IONotificationPortCreate(kIOMainPortDefault)
	if notifyPort == 0 {
		k.CFRelease(mode)
		return nil, ErrOther
	}
	source := hotplug.IONotificationPortGetRunLoopSource(notifyPort)
	if source == 0 {
		hotplug.IONotificationPortDestroy(notifyPort)
		k.CFRelease(mode)
		return nil, ErrOther
	}

	matchArrival := k.IOServiceMatching("IOUSBHostDevice\x00")
	matchTerminated := k.IOServiceMatching("IOUSBHostDevice\x00")
	if matchArrival == 0 || matchTerminated == 0 {
		hotplug.IONotificationPortDestroy(notifyPort)
		k.CFRelease(mode)
		return nil, ErrOther
	}

	var arrivalIter, terminatedIter uint32

	// seen tracks devices this registration has reported as arrived, keyed by
	// locationID, so a later termination can be matched back to the *Device
	// already built for it -- properties are often no longer readable by the
	// time a termination notification fires -- and so a device this
	// registration never reported (filtered out by vendorID/productID)
	// correctly never reports a departure either. It is touched only from the
	// registration's own goroutine, including the initial drain below, which
	// runs there and not on the caller's goroutine; see the happens-before
	// argument at the go statement further down.
	seen := make(map[uint32]*Device)

	matches := func(dev *Device) bool {
		if vendorID != 0 && dev.Descriptor.VendorID != vendorID {
			return false
		}
		if productID != 0 && dev.Descriptor.ProductID != productID {
			return false
		}
		return true
	}

	drainArrivals := func(iterator uint32) {
		for {
			service := k.IOIteratorNext(iterator)
			if service == 0 {
				return
			}
			dev, ok := k.deviceFromService(service)
			k.IOObjectRelease(service)
			if !ok || !matches(dev) {
				continue
			}
			seen[dev.IOKitDevice.LocationID] = dev
			callback(HotplugEventArrived, dev)
		}
	}

	drainTerminations := func(iterator uint32) {
		for {
			service := k.IOIteratorNext(iterator)
			if service == 0 {
				return
			}

			// A terminated service's other properties may already be gone;
			// locationID is read directly rather than through
			// deviceFromService, which also requires idVendor/idProduct to
			// still be readable.
			var loc uint32
			var props uintptr
			if r := k.IORegistryEntryCreateCFProperties(service, &props, 0, 0); r == kernSuccess && props != 0 {
				if v, ok := k.propertyNumber(props, propLocationID); ok {
					loc = uint32(v)
				}
				k.CFRelease(props)
			}
			k.IOObjectRelease(service)

			dev, ok := seen[loc]
			if !ok {
				continue
			}
			delete(seen, loc)
			callback(HotplugEventLeft, dev)
		}
	}

	arrivalCB := purego.NewCallback(func(refCon uintptr, iterator uint32) { drainArrivals(iterator) })
	terminatedCB := purego.NewCallback(func(refCon uintptr, iterator uint32) { drainTerminations(iterator) })

	if ret := hotplug.IOServiceAddMatchingNotification(
		notifyPort, kIOFirstMatchNotification, matchArrival, arrivalCB, 0, &arrivalIter); ret != kernSuccess {
		hotplug.IONotificationPortDestroy(notifyPort)
		k.CFRelease(mode)
		return nil, ErrOther
	}
	if ret := hotplug.IOServiceAddMatchingNotification(
		notifyPort, kIOTerminatedNotification, matchTerminated, terminatedCB, 0, &terminatedIter); ret != kernSuccess {
		k.IOObjectRelease(arrivalIter)
		hotplug.IONotificationPortDestroy(notifyPort)
		k.CFRelease(mode)
		return nil, ErrOther
	}

	h := &darwinHotplugHandle{
		ready: make(chan struct{}),
		done:  make(chan struct{}),
	}

	// Everything captured above (k, matchArrival/matchTerminated already
	// consumed, arrivalIter, terminatedIter, seen, drainArrivals,
	// drainTerminations, notifyPort, source, mode, h) is only written by the
	// calling goroutine before this statement and only read by the new
	// goroutine after it starts, which the go statement itself makes safe
	// without further synchronization.
	go func() {
		runtime.LockOSThread()
		// Deliberately never unlocked: this goroutine parks in
		// CFRunLoopRunInMode for its entire life and exits by returning, at
		// which point the runtime reclaims the thread along with the
		// goroutine.

		rl := hotplug.CFRunLoopGetCurrent()
		hotplug.CFRunLoopAddSource(rl, source, mode)

		h.mu.Lock()
		h.runLoop = rl
		h.mu.Unlock()
		close(h.ready)

		// Drain both iterators once to arm their notifications; the arrival
		// drain also reports every already-connected matching device, since
		// IOKit's initial iterator makes no distinction between those and a
		// genuinely new arrival.
		drainArrivals(arrivalIter)
		drainTerminations(terminatedIter)

		for {
			h.mu.Lock()
			stopped := h.stopped
			h.mu.Unlock()
			if stopped {
				break
			}

			// A long timeout: CFRunLoopStop, called from Deregister on
			// another goroutine, wakes this immediately rather than waiting
			// for it to elapse, and returnAfterSourceHandled means it also
			// returns promptly whenever a notification actually fires.
			hotplug.CFRunLoopRunInMode(mode, 3600, true)
		}

		k.IOObjectRelease(arrivalIter)
		k.IOObjectRelease(terminatedIter)
		// Also removes source and its underlying mach port.
		hotplug.IONotificationPortDestroy(notifyPort)
		k.CFRelease(mode)
		close(h.done)
	}()

	return h, nil
}
