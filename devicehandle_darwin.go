// DeviceHandle operations for the macOS backend.
//
// Opening a device, control transfers, claiming an interface and synchronous
// bulk/interrupt transfers are all implemented via vtable dispatch on
// IOUSBDeviceInterface and IOUSBInterfaceInterface (see
// device_interface_darwin.go and interface_darwin.go).
// Asynchronous bulk/interrupt and isochronous transfers are implemented too,
// in async_darwin.go and isochronous_darwin.go, which
// share one per-interface async pump. Nothing here returns a nil error to
// pretend an operation happened.
//
// Field names and method signatures are what transfer_darwin.go and
// compat_darwin.go, shared across every macOS build, expect, and what the
// contract assertions in api_contract.go check against.

package usb

import (
	"encoding/binary"
	"fmt"
	"time"
)

// --- DeviceHandle -------------------------------------------------------------

// Close closes the device handle, releasing exclusive access and the interface.
func (h *DeviceHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return nil
	}
	h.closed = true

	for _, intf := range h.interfaces {
		intf.closeInterface()
		intf.release()
	}
	h.interfaces = nil
	h.claimedIfaces = nil

	if h.hid != nil {
		h.hid.close()
		h.hid = nil
	}

	if h.devInterface != nil {
		h.devInterface.closeDevice()
		h.devInterface.release()
		h.devInterface = nil
	}
	return nil
}

// SetConfiguration selects a device configuration.
func (h *DeviceHandle) SetConfiguration(config int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed || h.devInterface == nil {
		return ErrDeviceNotFound
	}
	if config < 0 || config > 0xff {
		return ErrInvalidParameter
	}
	return h.devInterface.SetConfiguration(uint8(config))
}

// GetConfiguration returns the active configuration value.
func (h *DeviceHandle) GetConfiguration() (int, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || h.devInterface == nil {
		return 0, ErrDeviceNotFound
	}
	config, err := h.devInterface.Configuration()
	if err != nil {
		return 0, err
	}
	return int(config), nil
}

// ClaimInterface claims an interface for I/O.
//
// The interface's io_service_t is found via the device interface's
// CreateInterfaceIterator rather than by walking the IORegistry tree, matched
// against alternate setting 0: a freshly opened device has not selected any
// other alternate setting yet, and SetAltSetting moves it later.
func (h *DeviceHandle) ClaimInterface(iface uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed || h.devInterface == nil {
		return ErrDeviceNotFound
	}
	if h.claimedIfaces[iface] {
		return nil
	}

	service, err := findInterfaceService(h.devInterface, iface, 0)
	if err != nil {
		return err
	}
	defer releaseService(service)

	intf, err := openInterfaceInterface(service)
	if err != nil {
		return err
	}
	if err := intf.open(); err != nil {
		intf.release()

		// kIOReturnExclusiveAccess (mapped to ErrDeviceBusy by intf.open)
		// means IOUSBHIDDriver already has this interface open exclusively --
		// expected for a device that presents itself as HID rather than a
		// vendor interface, the same signal WinUsb_Initialize failing is on
		// Windows. Fall back to the IOHIDDevice transport, reachable as this
		// interface's own child in the registry. See hid_darwin.go.
		if err == ErrDeviceBusy {
			hidDev, hidErr := openHIDInterface(h, iface, service)
			if hidErr == nil {
				h.hid = hidDev
				h.claimedIfaces[iface] = true
				return nil
			}
			if hidErr == ErrNotSupported {
				return fmt.Errorf("interface exposes only pointing or keyboard collections: %w", ErrNotSupported)
			}
		}
		return err
	}

	h.interfaces[iface] = intf
	h.claimedIfaces[iface] = true
	return nil
}

// ReleaseInterface releases a claimed interface.
func (h *DeviceHandle) ReleaseInterface(iface uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}
	if !h.claimedIfaces[iface] {
		return nil
	}

	if intf, ok := h.interfaces[iface]; ok {
		intf.closeInterface()
		intf.release()
		delete(h.interfaces, iface)
	}
	if h.hid != nil && h.hid.ownsInterface(iface) {
		h.hid.close()
		h.hid = nil
	}
	delete(h.claimedIfaces, iface)
	return nil
}

// SetAltSetting selects an alternate setting on an interface.
func (h *DeviceHandle) SetAltSetting(iface, altSetting uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}
	intf, ok := h.interfaces[iface]
	if !ok {
		return fmt.Errorf("interface %d not claimed", iface)
	}
	return intf.SetAlternateSetting(altSetting)
}

// ClearHalt clears a stall condition on an endpoint.
//
// The interface owning the endpoint is not tracked, so this tries every
// claimed interface until one accepts the clear.
func (h *DeviceHandle) ClearHalt(endpoint uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrDeviceNotFound
	}
	for _, intf := range h.interfaces {
		pipeRef, err := intf.PipeRefForEndpoint(endpoint)
		if err != nil {
			continue
		}
		if err := intf.ClearPipeStall(pipeRef); err == nil {
			return nil
		}
	}
	return fmt.Errorf("endpoint %02x not found", endpoint)
}

// ResetDevice resets the device.
func (h *DeviceHandle) ResetDevice() error { return ErrNotSupported }

// ResetEndpoint resets an endpoint.
func (h *DeviceHandle) ResetEndpoint(endpoint uint8) error { return ErrNotSupported }

// KernelDriverActive reports whether a kernel driver holds the interface.
//
// IOKit does not expose driver bindings the way Linux does; a claimed interface
// is the only signal, and it surfaces as ClaimInterface failing.
func (h *DeviceHandle) KernelDriverActive(iface uint8) (bool, error) {
	return false, ErrNotSupported
}

// DetachKernelDriver detaches the kernel driver from an interface.
//
// macOS provides no user-space way to unbind a kernel driver, so this always
// reports ErrNotSupported rather than pretending to have detached anything.
func (h *DeviceHandle) DetachKernelDriver(iface uint8) error { return ErrNotSupported }

// AttachKernelDriver re-attaches the kernel driver to an interface.
//
// macOS provides no user-space way to rebind a kernel driver.
func (h *DeviceHandle) AttachKernelDriver(iface uint8) error { return ErrNotSupported }

// StringDescriptor reads a string descriptor from the device.
//
// Enumeration also caches the manufacturer, product and serial strings from the
// IOKit registry, so Device.SysfsStrings carries those without an open device.
func (h *DeviceHandle) StringDescriptor(index uint8) (string, error) {
	if index == 0 {
		return "", nil
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || h.devInterface == nil {
		return "", ErrDeviceNotFound
	}

	// A GET_DESCRIPTOR request for a string, in English (US).
	buf := make([]byte, 255)
	n, err := h.devInterface.ControlTransfer(0x80, USB_REQ_GET_DESCRIPTOR,
		descriptorRequestValue(USB_DT_STRING, index), 0x0409, buf, 5000)
	if err != nil {
		return "", err
	}
	return decodeStringDescriptor(buf[:n]), nil
}

// GetDeviceDescriptor returns the device descriptor.
func (h *DeviceHandle) GetDeviceDescriptor() (*DeviceDescriptor, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || h.device == nil {
		return nil, ErrDeviceNotFound
	}
	desc := h.device.Descriptor
	return &desc, nil
}

// GetActiveConfigDescriptor returns the descriptor for the active configuration.
func (h *DeviceHandle) GetActiveConfigDescriptor() (*ConfigDescriptor, error) {
	return nil, ErrNotSupported
}

// GetConfigDescriptor returns a configuration descriptor by index.
func (h *DeviceHandle) GetConfigDescriptor(index uint8) (*ConfigDescriptor, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || h.devInterface == nil {
		return nil, ErrDeviceNotFound
	}
	return fetchConfigDescriptor(h.devInterface, index)
}

// fetchConfigDescriptor reads and parses a configuration descriptor directly
// off dev, with no locking of its own.
//
// Split out of GetConfigDescriptor so that ClaimInterface's HID fallback
// (openHIDInterface in hid_darwin.go) can read the descriptor too: it runs
// while ClaimInterface already holds h.mu.Lock(), and GetConfigDescriptor's
// own h.mu.RLock() would deadlock against that.
func fetchConfigDescriptor(dev *IOUSBDeviceInterface, index uint8) (*ConfigDescriptor, error) {
	// Read the 9-byte header first to learn wTotalLength, then re-read the
	// whole descriptor now that its size is known.
	header := make([]byte, 9)
	_, err := dev.ControlTransfer(0x80, USB_REQ_GET_DESCRIPTOR,
		descriptorRequestValue(USB_DT_CONFIG, index), 0, header, 5000)
	if err != nil {
		return nil, err
	}

	totalLength := binary.LittleEndian.Uint16(header[2:4])
	if totalLength < 9 {
		return nil, fmt.Errorf("config descriptor too short: %d bytes", totalLength)
	}

	full := make([]byte, totalLength)
	_, err = dev.ControlTransfer(0x80, USB_REQ_GET_DESCRIPTOR,
		descriptorRequestValue(USB_DT_CONFIG, index), 0, full, 5000)
	if err != nil {
		return nil, err
	}

	config := &ConfigDescriptor{}
	if err := config.Unmarshal(full); err != nil {
		return nil, err
	}
	return config, nil
}

// GetBOSDescriptor reads the Binary Object Store descriptor.
func (h *DeviceHandle) GetBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error) {
	return nil, nil, ErrNotSupported
}

// GetDeviceQualifierDescriptor reads the device qualifier descriptor.
func (h *DeviceHandle) GetDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error) {
	return nil, ErrNotSupported
}

// GetCapabilities returns platform capability bits, which IOKit does not expose.
func (h *DeviceHandle) GetCapabilities() (uint32, error) { return 0, ErrNotSupported }

// GetSpeed returns the device's negotiated speed.
func (h *DeviceHandle) GetSpeed() (Speed, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed || h.devInterface == nil {
		return SpeedUnknown, ErrDeviceNotFound
	}
	raw, err := h.devInterface.DeviceSpeed()
	if err != nil {
		return SpeedUnknown, err
	}
	return iokitSpeedToSpeed(raw), nil
}

// HandleEvents services pending asynchronous transfer completions.
//
// A no-op here: every interface with an async transfer in flight (bulk,
// interrupt or isochronous) runs its own background pump goroutine, started
// automatically on first use -- see ensureAsyncPump in isochronous_darwin.go.
// There is nothing for the caller to drive. Kept so portable code that calls
// it unconditionally keeps working.
func HandleEvents(timeout time.Duration) error { return nil }

// RunEventLoop services asynchronous completions until stop is closed. See
// HandleEvents: there is nothing to pump here, so this just waits for stop.
func RunEventLoop(stop <-chan struct{}) { <-stop }

// AsyncTransfer and IsochronousTransfer, and their shared per-interface async
// pump, live in async_darwin.go and
// isochronous_darwin.go.
