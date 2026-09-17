package usb

// registerHotplugCallback is not implemented on Linux yet.
//
// The natural mechanism is a netlink uevent socket (or an inotify watch on
// /dev/bus/usb combined with a rescan), neither of which has been exercised
// against real hardware from this codebase, so this reports ErrNotSupported
// rather than a handle that silently never calls back.
func registerHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	return nil, ErrNotSupported
}
