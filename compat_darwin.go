package usb

import (
	"fmt"
	"time"
)

// Compatibility methods for macOS to match Linux API

// Descriptor returns the device descriptor
func (h *DeviceHandle) Descriptor() DeviceDescriptor {
	if h.device != nil {
		return h.device.Descriptor
	}
	return DeviceDescriptor{}
}

// Configuration gets the current configuration
func (h *DeviceHandle) Configuration() (int, error) {
	return h.GetConfiguration()
}

// ConfigDescriptorByValue gets a configuration descriptor by value
func (h *DeviceHandle) ConfigDescriptorByValue(value uint8) (*ConfigDescriptor, error) {
	// On macOS, we'll use index-based lookup
	// This is a simplification - ideally we'd iterate to find the right config
	return h.GetConfigDescriptor(value - 1)
}

// RawConfigDescriptor returns the raw configuration descriptor bytes
func (h *DeviceHandle) RawConfigDescriptor(index uint8) ([]byte, error) {
	// Not directly supported, would need to capture raw bytes during parsing
	return nil, fmt.Errorf("raw config descriptor not implemented on macOS")
}

// SetInterfaceAltSetting sets the alternate setting for an interface
func (h *DeviceHandle) SetInterfaceAltSetting(iface, altSetting uint8) error {
	return h.SetAltSetting(iface, altSetting)
}

// Status gets device/interface/endpoint status
func (h *DeviceHandle) Status(requestType uint8, index uint16) (uint16, error) {
	recipient := uint16(requestType & 0x1F)
	return h.GetStatus(recipient, index)
}

// Interface gets the current alternate setting for an interface
func (h *DeviceHandle) Interface(iface uint8) (uint8, error) {
	// Not directly supported on macOS
	return 0, fmt.Errorf("get interface not implemented on macOS")
}

// RawDescriptor reads a raw descriptor
func (h *DeviceHandle) RawDescriptor(descType, descIndex uint8, langID uint16, data []byte) (int, error) {
	// Use control transfer to get descriptor
	value := (uint16(descType) << 8) | uint16(descIndex)
	return h.ControlTransfer(
		0x80, // Device-to-host, standard, device
		USB_REQ_GET_DESCRIPTOR,
		value,
		langID,
		data,
		5*time.Second,
	)
}

// SetDescriptor sets a descriptor
func (h *DeviceHandle) SetDescriptor(descType, descIndex uint8, langID uint16, data []byte) error {
	value := (uint16(descType) << 8) | uint16(descIndex)
	_, err := h.ControlTransfer(
		0x00, // Host-to-device, standard, device
		USB_REQ_SET_DESCRIPTOR,
		value,
		langID,
		data,
		5*time.Second,
	)
	return err
}

// SynchFrame synchronizes frame
func (h *DeviceHandle) SynchFrame(endpoint uint8) (uint16, error) {
	buf := make([]byte, 2)
	_, err := h.ControlTransfer(
		0x82, // Device-to-host, standard, endpoint
		USB_REQ_SYNCH_FRAME,
		0,
		uint16(endpoint),
		buf,
		5*time.Second,
	)
	if err != nil {
		return 0, err
	}
	return uint16(buf[0]) | (uint16(buf[1]) << 8), nil
}

// Capabilities returns device capabilities
func (h *DeviceHandle) Capabilities() (uint32, error) {
	return h.GetCapabilities()
}

// Speed returns the device speed
func (h *DeviceHandle) Speed() (uint8, error) {
	speed, err := h.GetSpeed()
	return uint8(speed), err
}

// SSEndpointCompanionDescriptor gets SuperSpeed endpoint companion descriptor
func (h *DeviceHandle) SSEndpointCompanionDescriptor(configIndex, interfaceNumber, altSetting, endpointAddress uint8) (*SuperSpeedEndpointCompanionDescriptor, error) {
	// Would need to parse from config descriptor
	return nil, fmt.Errorf("SS endpoint companion descriptor not implemented on macOS")
}

// SSUSBDeviceCapabilityDescriptor gets SuperSpeed USB device capability descriptor
func (h *DeviceHandle) SSUSBDeviceCapabilityDescriptor() (*SuperSpeedUSBCapability, error) {
	bos, caps, err := h.GetBOSDescriptor()
	if err != nil {
		return nil, err
	}

	_ = bos // unused

	// Look for SuperSpeed capability
	for _, cap := range caps {
		if cap.DevCapabilityType == 0x03 { // SuperSpeed USB
			// Would need to parse the capability data
			return &SuperSpeedUSBCapability{
				Length:            cap.Length,
				DescriptorType:    cap.DescriptorType,
				DevCapabilityType: cap.DevCapabilityType,
			}, nil
		}
	}

	return nil, fmt.Errorf("SuperSpeed USB capability not found")
}

// USB20ExtensionDescriptor gets USB 2.0 extension descriptor
func (h *DeviceHandle) USB20ExtensionDescriptor() (*USB2ExtensionCapability, error) {
	bos, caps, err := h.GetBOSDescriptor()
	if err != nil {
		return nil, err
	}

	_ = bos // unused

	// Look for USB 2.0 extension capability
	for _, cap := range caps {
		if cap.DevCapabilityType == 0x02 { // USB 2.0 extension
			// Would need to parse the capability data
			return &USB2ExtensionCapability{
				Length:            cap.Length,
				DescriptorType:    cap.DescriptorType,
				DevCapabilityType: cap.DevCapabilityType,
			}, nil
		}
	}

	return nil, fmt.Errorf("USB 2.0 extension capability not found")
}

// ReadBOSDescriptor reads the BOS descriptor
func (h *DeviceHandle) ReadBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error) {
	return h.GetBOSDescriptor()
}

// ReadDeviceQualifierDescriptor reads the device qualifier descriptor
func (h *DeviceHandle) ReadDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error) {
	return h.GetDeviceQualifierDescriptor()
}

// ReadConfigDescriptor reads configuration descriptor with interfaces and endpoints
func (h *DeviceHandle) ReadConfigDescriptor(index uint8) (*ConfigDescriptor, []InterfaceDescriptor, []EndpointDescriptor, error) {
	config, err := h.GetConfigDescriptor(index)
	if err != nil {
		return nil, nil, nil, err
	}

	// Flatten interfaces and endpoints for backward compatibility
	var interfaces []InterfaceDescriptor
	var endpoints []EndpointDescriptor

	for _, iface := range config.Interfaces {
		for _, altSetting := range iface.AltSettings {
			// Convert InterfaceAltSetting to InterfaceDescriptor
			interfaces = append(interfaces, InterfaceDescriptor{
				Length:            altSetting.Length,
				DescriptorType:    altSetting.DescriptorType,
				InterfaceNumber:   altSetting.InterfaceNumber,
				AlternateSetting:  altSetting.AlternateSetting,
				NumEndpoints:      altSetting.NumEndpoints,
				InterfaceClass:    altSetting.InterfaceClass,
				InterfaceSubClass: altSetting.InterfaceSubClass,
				InterfaceProtocol: altSetting.InterfaceProtocol,
				InterfaceIndex:    altSetting.InterfaceIndex,
			})

			for _, ep := range altSetting.Endpoints {
				// Convert Endpoint to EndpointDescriptor
				endpoints = append(endpoints, EndpointDescriptor{
					Length:         ep.Length,
					DescriptorType: ep.DescriptorType,
					EndpointAddr:   ep.EndpointAddr,
					Attributes:     ep.Attributes,
					MaxPacketSize:  ep.MaxPacketSize,
					Interval:       ep.Interval,
				})
			}
		}
	}

	return config, interfaces, endpoints, nil
}

// Device returns the device associated with this handle
func (h *DeviceHandle) Device() *Device {
	return h.device
}

// NewIsochronousTransfer creates a new isochronous transfer
func (h *DeviceHandle) NewIsochronousTransfer(endpoint uint8, numPackets, packetSize int) (*IsochronousTransfer, error) {
	return NewIsochronousTransfer(h, endpoint, numPackets, packetSize), nil
}

// BulkTransferWithOptions performs a bulk transfer, optionally allowing a
// zero-length packet.
//
// Matching the Linux backend, an empty data slice is rejected with
// ErrInvalidParameter unless allowZeroLength is true.
func (h *DeviceHandle) BulkTransferWithOptions(endpoint uint8, data []byte, timeout time.Duration, allowZeroLength bool) (int, error) {
	return h.bulkTransfer(endpoint, data, timeout, allowZeroLength)
}

// InterruptTransferWithRetry performs an interrupt transfer with retry (simplified implementation)
func (h *DeviceHandle) InterruptTransferWithRetry(endpoint uint8, data []byte, timeout time.Duration, retries int) (int, error) {
	var lastErr error
	for i := 0; i <= retries; i++ {
		n, err := h.InterruptTransfer(endpoint, data, timeout)
		if err == nil {
			return n, nil
		}
		lastErr = err
		if err != ErrTimeout {
			break
		}
	}
	return 0, lastErr
}

// SetShortPacketMode configures whether short packets are treated as errors.
//
// This is a usbfs feature with no IOKit counterpart, so it reports
// ErrNotSupported.
func (h *DeviceHandle) SetShortPacketMode(enabled bool) error {
	return ErrNotSupported
}

// SubmitHighBandwidthIso submits a high-bandwidth isochronous transfer.
//
// IOKit exposes no equivalent of the usbfs high-bandwidth iso path, so this
// reports ErrNotSupported. Use NewIsochronousTransfer instead.
func (h *DeviceHandle) SubmitHighBandwidthIso(transfer *HighBandwidthIsoTransfer, callback func([]byte, error)) error {
	return ErrNotSupported
}

// --- Canonical asynchronous transfer API -------------------------------------
//
// These live here, rather than in the CGO-dependent files, so that they stay
// aligned with the cross-platform contract asserted in api_contract_unix.go.

// NewBulkTransfer creates an asynchronous bulk transfer.
func (h *DeviceHandle) NewBulkTransfer(endpoint uint8, bufferSize int) (*AsyncTransfer, error) {
	return h.newAsyncTransfer(endpoint, TransferTypeBulk, bufferSize)
}

// NewInterruptTransfer creates an asynchronous interrupt transfer.
func (h *DeviceHandle) NewInterruptTransfer(endpoint uint8, bufferSize int) (*AsyncTransfer, error) {
	return h.newAsyncTransfer(endpoint, TransferTypeInterrupt, bufferSize)
}

// NewControlTransfer creates an asynchronous control transfer.
func (h *DeviceHandle) NewControlTransfer(bufferSize int) (*AsyncTransfer, error) {
	return h.newAsyncTransfer(0, TransferTypeControl, bufferSize)
}

func (h *DeviceHandle) newAsyncTransfer(endpoint uint8, transferType TransferType, bufferSize int) (*AsyncTransfer, error) {
	h.mu.RLock()
	closed := h.closed
	h.mu.RUnlock()

	if closed {
		return nil, ErrDeviceNotFound
	}
	if bufferSize < 0 {
		return nil, ErrInvalidParameter
	}
	return NewAsyncTransfer(h, endpoint, transferType, bufferSize), nil
}

// Wait blocks until the transfer completes.
func (t *AsyncTransfer) Wait() error {
	if t.done == nil {
		return ErrInvalidParameter
	}
	<-t.done
	return nil
}

// WaitWithTimeout blocks until the transfer completes or the timeout elapses,
// in which case it returns ErrTimeout.
func (t *AsyncTransfer) WaitWithTimeout(timeout time.Duration) error {
	if t.done == nil {
		return ErrInvalidParameter
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-t.done:
		return nil
	case <-timer.C:
		return ErrTimeout
	}
}

// markCompletedLocked records that the transfer has finished and releases any
// waiters. The caller must hold t.mutex.
//
// IOKit signals completion from a CFRunLoop callback, so waiters cannot hold the
// mutex while blocking. Closing a channel lets them wait without polling and
// without a goroutine per waiter, and closing it under the mutex on the
// false-to-true transition guarantees it happens exactly once.
func (t *AsyncTransfer) markCompletedLocked() {
	if t.completed {
		return
	}
	t.completed = true
	if t.done != nil {
		close(t.done)
	}
}

// Fill copies data into the transfer's buffer, resizing it if necessary.
func (t *AsyncTransfer) Fill(data []byte) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.submitted && !t.completed {
		return ErrDeviceBusy
	}

	if len(data) > len(t.buffer) {
		t.buffer = make([]byte, len(data))
	} else {
		t.buffer = t.buffer[:len(data)]
	}
	copy(t.buffer, data)
	return nil
}

// --- Canonical isochronous transfer API --------------------------------------

// Buffer returns the transfer's data buffer.
func (t *IsochronousTransfer) Buffer() []byte {
	return t.buffer
}

// Packets returns a descriptor per isochronous packet.
func (t *IsochronousTransfer) Packets() []IsoPacketDescriptor {
	packets := make([]IsoPacketDescriptor, t.numPackets)
	for i := range packets {
		packets[i] = IsoPacketDescriptor{
			Length:       uint32(t.packetSize),
			ActualLength: uint32(t.packetLengths[i]),
			Status:       int32(t.packetStatuses[i]),
		}
	}
	return packets
}

// IsoPacketBuffer returns the data for a single isochronous packet.
func (t *IsochronousTransfer) IsoPacketBuffer(packetIndex int) ([]byte, error) {
	if packetIndex < 0 || packetIndex >= t.numPackets {
		return nil, ErrInvalidParameter
	}

	start := packetIndex * t.packetSize
	end := start + t.packetLengths[packetIndex]
	if end > len(t.buffer) {
		end = len(t.buffer)
	}
	if start > end {
		return nil, ErrInvalidParameter
	}
	return t.buffer[start:end], nil
}

// IsoPacketBufferSlices returns one slice per isochronous packet.
func (t *IsochronousTransfer) IsoPacketBufferSlices() [][]byte {
	slices := make([][]byte, 0, t.numPackets)
	for i := 0; i < t.numPackets; i++ {
		buf, err := t.IsoPacketBuffer(i)
		if err != nil {
			break
		}
		slices = append(slices, buf)
	}
	return slices
}
