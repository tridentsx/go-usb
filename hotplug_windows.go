package usb

// registerHotplugCallback is not implemented on Windows yet.
//
// The natural mechanism is RegisterDeviceNotification plus a WM_DEVICECHANGE
// message pump on a hidden window, which has not been exercised against real
// hardware from this codebase, so this reports ErrNotSupported rather than a
// handle that silently never calls back.
func registerHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	return nil, ErrNotSupported
}
