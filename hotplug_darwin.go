//go:build darwin && cgo

package usb

// registerHotplugCallback is not implemented in the cgo backend.
//
// The cgo backend is being phased out in favor of the CGO-free one (see
// purego_hotplug_darwin.go and issue #14), so this reports ErrNotSupported
// rather than duplicating the IOKit notification-port machinery in code that
// is going away.
func registerHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	return nil, ErrNotSupported
}
