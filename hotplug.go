package usb

// HotplugEvent describes what happened to a device.
type HotplugEvent int

const (
	// HotplugEventArrived reports that a matching device was connected.
	HotplugEventArrived HotplugEvent = iota
	// HotplugEventLeft reports that a matching device was disconnected.
	HotplugEventLeft
)

func (e HotplugEvent) String() string {
	switch e {
	case HotplugEventArrived:
		return "arrived"
	case HotplugEventLeft:
		return "left"
	default:
		return "unknown"
	}
}

// HotplugCallback is invoked when a device matching a RegisterHotplugCallback
// call arrives or leaves.
//
// It runs on a goroutine this package owns, one per registration, never on
// the caller's own goroutine and never synchronously inside platform callback
// machinery. It must not block for long: doing so delays delivery of further
// events for that registration, and on macOS starves the run loop the
// registration depends on to notice anything else.
type HotplugCallback func(event HotplugEvent, device *Device)

// HotplugHandle represents an active hotplug watch. Deregister stops it and
// releases whatever platform resources it holds; calling it more than once is
// safe.
type HotplugHandle interface {
	Deregister() error
}

// RegisterHotplugCallback watches for USB devices arriving or leaving and
// invokes callback for each one that matches.
//
// vendorID and productID of 0 match any vendor or product respectively, so
// RegisterHotplugCallback(0, 0, cb) watches every device.
//
// Every platform backend implements this; one that cannot watch for hotplug
// events yet returns ErrNotSupported rather than a handle that never calls
// back. See the platform-specific hotplug_*.go files.
func RegisterHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	return registerHotplugCallback(vendorID, productID, callback)
}
