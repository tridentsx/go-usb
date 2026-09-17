package usb

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DeviceListOption is a functional option for configuring DeviceList behavior.
type DeviceListOption func(*deviceListOptions)

// deviceListOptions holds the configuration for DeviceList.
type deviceListOptions struct {
	includeInaccessible bool
}

// WithInaccessibleDevices returns an option that includes devices that cannot
// be opened.
//
// Deprecated: inaccessible devices are now always listed, matching the Linux
// and macOS backends, so this option has no effect. It is retained for API
// compatibility.
func WithInaccessibleDevices() DeviceListOption {
	return func(o *deviceListOptions) {
		o.includeInaccessible = true
	}
}

// DeviceList returns a list of all USB devices on the system.
//
// Devices are found with SetupAPI and then described by asking the hub each one
// is attached to, which yields the device descriptor, configuration
// descriptors, cached strings, bus address and speed without opening the
// device. That works whatever function driver owns the device, so HID and
// class-driver devices are described as fully as WinUSB ones.
//
// Opening a device still requires it to be bound to WinUSB, so Device.Open may
// fail for entries listed here.
//
// Bus numbers are synthetic: Windows has no bus number, so root hubs are
// numbered in a stable order. Addresses are the real USB device addresses
// reported by the hub.
func DeviceList(opts ...DeviceListOption) ([]*Device, error) {
	// Options are accepted for API compatibility; see WithInaccessibleDevices.
	options := &deviceListOptions{}
	for _, opt := range opts {
		opt(options)
	}

	winDevices, err := EnumerateUSBDevices()
	if err != nil {
		return nil, err
	}

	// Locate every interface on the USB tree first, so that duplicates can be
	// collapsed and bus numbers assigned before any hub is queried.
	type located struct {
		wd  *WindowsUSBDevice
		loc deviceLocation
		ok  bool
	}

	entries := make([]located, 0, len(winDevices))
	roots := make(map[uint32]bool)
	for _, wd := range winDevices {
		e := located{wd: wd}
		if loc, err := locateDevice(wd.DevInst); err == nil {
			e.loc, e.ok = loc, true
			roots[loc.RootInst] = true
		}
		entries = append(entries, e)
	}

	// Also seed the roots map with every root hub the host controllers report,
	// so that buses with no WinUSB/generic-class devices still get a bus number
	// and root hubs themselves appear in the list.
	rhDevnodes := enumerateRootHubDevnodes()
	for _, devInst := range rhDevnodes {
		roots[devInst] = true
	}

	buses := busNumbering(roots)

	// A composite device is reachable both through its own device interface and
	// through each function interface, and they all resolve to the same hub
	// port. Collapse them to one entry per port so the list has one line per
	// physical device, while remembering which path can actually be opened:
	// identity comes from the device interface, openability from WinUSB.
	type merged struct {
		displayPath string
		openPath    string
		devInst     uint32
		loc         deviceLocation
	}

	byPort := make(map[string]*merged)
	var order []string
	for _, e := range entries {
		if !e.ok {
			continue
		}

		key := strings.ToLower(e.loc.HubPath) + "/" + strconv.Itoa(e.loc.Port)
		m, seen := byPort[key]
		if !seen {
			m = &merged{loc: e.loc}
			byPort[key] = m
			order = append(order, key)
		}

		// Prefer the device interface for identity, since a function interface
		// path describes only part of a composite device.
		if m.displayPath == "" || (isFunctionInterfacePath(m.displayPath) && !isFunctionInterfacePath(e.wd.DevicePath)) {
			m.displayPath = e.wd.DevicePath
			// The devnode of the device interface is the one HID collections
			// hang below, so it must travel with the display path.
			m.devInst = e.wd.DevInst
		}
		// Only a WinUSB interface can be opened for I/O.
		if m.openPath == "" && e.wd.WinUSB {
			m.openPath = e.wd.DevicePath
		}
	}

	devices := make([]*Device, 0, len(order))
	hubs := newHubCache()
	defer hubs.closeAll()

	for _, key := range order {
		m := byPort[key]

		device := newDeviceFromPath(m.displayPath)
		device.devInst = m.devInst
		if m.openPath != "" {
			// Open must target the WinUSB interface, which is not necessarily
			// the path the device is identified by.
			device.devicePath = m.openPath
		}
		device.Bus = buses[m.loc.RootInst]

		if hub, err := hubs.get(m.loc.HubPath); err == nil {
			describeFromHub(device, hub, m.loc.Port)
		}
		devices = append(devices, device)
	}

	// Anything whose position on the tree could not be established is still
	// reported, identified by whatever its path reveals.
	for _, e := range entries {
		if !e.ok {
			devices = append(devices, newDeviceFromPath(e.wd.DevicePath))
		}
	}

	// Append one entry per root hub. Root hubs are not returned by
	// EnumerateUSBDevices (their paths contain "root_hub" and no VID/PID) so
	// they must be described separately. Each gets Address=1 and DeviceClass=9
	// so that lsusb -t can identify it as the root of its bus.
	for _, devInst := range rhDevnodes {
		if dev := describeRootHub(devInst, buses[devInst]); dev != nil {
			devices = append(devices, dev)
		}
	}

	return devices, nil
}

// isFunctionInterfacePath reports whether a device path refers to one function
// of a composite device rather than the device itself.
func isFunctionInterfacePath(path string) bool {
	return strings.Contains(strings.ToLower(path), "&mi_")
}

// newDeviceFromPath builds a Device with only what the path itself reveals.
func newDeviceFromPath(devicePath string) *Device {
	vid, pid := parseVidPidFromPath(devicePath)
	return &Device{
		Path:       devicePath,
		devicePath: devicePath,
		Descriptor: DeviceDescriptor{VendorID: vid, ProductID: pid},
	}
}

// describeFromHub fills in a device's descriptors, address and strings by
// querying the hub it is attached to.
func describeFromHub(device *Device, hub windows.Handle, port int) {
	nc, err := hubNodeConnection(hub, port)
	if err != nil || !nc.Connected {
		return
	}

	device.Address = uint8(nc.DeviceAddress)
	if nc.Descriptor.Length == 18 {
		device.Descriptor = nc.Descriptor
	}

	device.SysfsStrings = &DeviceStrings{
		Manufacturer: hubStringDescriptor(hub, port, device.Descriptor.ManufacturerIndex),
		Product:      hubStringDescriptor(hub, port, device.Descriptor.ProductIndex),
		Serial:       hubStringDescriptor(hub, port, device.Descriptor.SerialNumberIndex),
	}

	for i := uint8(0); i < device.Descriptor.NumConfigurations; i++ {
		raw, err := hubConfigDescriptor(hub, port, i)
		if err != nil || len(raw) < 9 {
			continue
		}
		device.rawConfigs = append(device.rawConfigs, raw)
		device.Configs = append(device.Configs, RawConfigDescriptor{
			Length:             raw[0],
			DescriptorType:     raw[1],
			TotalLength:        binary.LittleEndian.Uint16(raw[2:4]),
			NumInterfaces:      raw[4],
			ConfigurationValue: raw[5],
			ConfigurationIndex: raw[6],
			Attributes:         raw[7],
			MaxPower:           raw[8],
		})
	}
}

// hubCache keeps hub handles open for the duration of one enumeration, since
// several devices usually share a hub.
type hubCache struct {
	handles map[string]windows.Handle
}

func newHubCache() *hubCache {
	return &hubCache{handles: make(map[string]windows.Handle)}
}

func (c *hubCache) get(path string) (windows.Handle, error) {
	if h, ok := c.handles[path]; ok {
		if h == windows.InvalidHandle {
			return h, ErrNotFound
		}
		return h, nil
	}

	h, err := openHubForQuery(path)
	if err != nil {
		c.handles[path] = windows.InvalidHandle
		return windows.InvalidHandle, err
	}
	c.handles[path] = h
	return h, nil
}

func (c *hubCache) closeAll() {
	for _, h := range c.handles {
		if h != windows.InvalidHandle {
			windows.CloseHandle(h)
		}
	}
}

// parseVidPidFromPath extracts VID and PID from Windows device path
func parseVidPidFromPath(path string) (vid, pid uint16) {
	// Windows device paths look like:
	// \\?\usb#vid_1234&pid_5678#...
	pathLower := strings.ToLower(path)

	vidIdx := strings.Index(pathLower, "vid_")
	if vidIdx >= 0 && vidIdx+8 <= len(pathLower) {
		if v, err := parseHex4(pathLower[vidIdx+4 : vidIdx+8]); err == nil {
			vid = v
		}
	}

	pidIdx := strings.Index(pathLower, "pid_")
	if pidIdx >= 0 && pidIdx+8 <= len(pathLower) {
		if p, err := parseHex4(pathLower[pidIdx+4 : pidIdx+8]); err == nil {
			pid = p
		}
	}

	return
}

func parseHex4(s string) (uint16, error) {
	var result uint16
	for _, c := range s {
		result <<= 4
		switch {
		case c >= '0' && c <= '9':
			result |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			result |= uint16(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			result |= uint16(c - 'A' + 10)
		default:
			return 0, fmt.Errorf("invalid hex character: %c", c)
		}
	}
	return result, nil
}

// OpenDevice opens a USB device by vendor ID and product ID.
// Returns the first matching device found.
func OpenDevice(vid, pid uint16) (*DeviceHandle, error) {
	devices, err := DeviceList()
	if err != nil {
		return nil, err
	}

	for _, dev := range devices {
		if dev.Descriptor.VendorID == vid && dev.Descriptor.ProductID == pid {
			return dev.Open()
		}
	}
	return nil, ErrDeviceNotFound
}

// devicePathRegex matches Windows USB device paths
var devicePathRegex = regexp.MustCompile(`(?i)\\\\[?]\\usb#vid_[0-9a-f]{4}&pid_[0-9a-f]{4}`)

// IsValidDevicePath checks if the given path is a valid USB device path.
func IsValidDevicePath(path string) bool {
	return devicePathRegex.MatchString(path)
}

// GetConfiguration gets the current device configuration
func (h *DeviceHandle) GetConfiguration() (int, error) {
	return h.Configuration()
}

// GetConfigDescriptor gets a configuration descriptor by index
func (h *DeviceHandle) GetConfigDescriptor(index uint8) (*ConfigDescriptor, error) {
	return h.ConfigDescriptorByValue(index + 1)
}

// GetActiveConfigDescriptor gets the descriptor for the active configuration
func (h *DeviceHandle) GetActiveConfigDescriptor() (*ConfigDescriptor, error) {
	config, err := h.GetConfiguration()
	if err != nil {
		return nil, err
	}

	if config > 0 {
		return h.ConfigDescriptorByValue(uint8(config))
	}

	return h.ConfigDescriptorByValue(1)
}

// GetDeviceDescriptor returns the device descriptor
func (h *DeviceHandle) GetDeviceDescriptor() (*DeviceDescriptor, error) {
	desc := h.Descriptor()
	return &desc, nil
}

// SetAltSetting sets the alternate setting for an interface
func (h *DeviceHandle) SetAltSetting(iface, altSetting uint8) error {
	return h.SetInterfaceAltSetting(iface, altSetting)
}

// KernelDriverActive checks if a kernel driver is active
func (h *DeviceHandle) KernelDriverActive(iface uint8) (bool, error) {
	// On Windows with WinUSB, the WinUSB driver is always active
	return false, nil
}

// GetBOSDescriptor gets the BOS descriptor
func (h *DeviceHandle) GetBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error) {
	return h.ReadBOSDescriptor()
}

// ReadBOSDescriptor reads the Binary Object Store descriptor
func (h *DeviceHandle) ReadBOSDescriptor() (*BOSDescriptor, []DeviceCapabilityDescriptor, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, nil, ErrDeviceNotFound
	}

	// First get header
	buf := make([]byte, 5)
	var transferred uint32

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(USB_DT_BOS),
		uintptr(0),
		uintptr(0),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return nil, nil, fmt.Errorf("failed to get BOS descriptor: %w", e1)
	}

	if transferred < 5 || buf[1] != USB_DT_BOS {
		return nil, nil, fmt.Errorf("invalid BOS descriptor")
	}

	bos := &BOSDescriptor{
		Length:         buf[0],
		DescriptorType: buf[1],
		TotalLength:    binary.LittleEndian.Uint16(buf[2:4]),
		NumDeviceCaps:  buf[4],
	}

	// Get full descriptor
	fullBuf := make([]byte, bos.TotalLength)
	r0, _, e1 = syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(USB_DT_BOS),
		uintptr(0),
		uintptr(0),
		uintptr(unsafe.Pointer(&fullBuf[0])),
		uintptr(len(fullBuf)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return nil, nil, fmt.Errorf("failed to get full BOS descriptor: %w", e1)
	}

	// Parse device capabilities
	caps := make([]DeviceCapabilityDescriptor, 0, bos.NumDeviceCaps)
	pos := 5

	for i := 0; i < int(bos.NumDeviceCaps) && pos < len(fullBuf); i++ {
		if pos+3 > len(fullBuf) {
			break
		}

		length := int(fullBuf[pos])
		if length < 3 || pos+length > len(fullBuf) {
			break
		}

		cap := DeviceCapabilityDescriptor{
			Length:            fullBuf[pos],
			DescriptorType:    fullBuf[pos+1],
			DevCapabilityType: fullBuf[pos+2],
		}

		caps = append(caps, cap)
		pos += length
	}

	return bos, caps, nil
}

// GetDeviceQualifierDescriptor gets the device qualifier descriptor
func (h *DeviceHandle) GetDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error) {
	return h.ReadDeviceQualifierDescriptor()
}

// ReadDeviceQualifierDescriptor reads device qualifier descriptor
func (h *DeviceHandle) ReadDeviceQualifierDescriptor() (*DeviceQualifierDescriptor, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrDeviceNotFound
	}

	buf := make([]byte, 10)
	var transferred uint32

	r0, _, e1 := syscall.SyscallN(
		procWinUsb_GetDescriptor.Addr(),
		uintptr(h.winusbHandle),
		uintptr(USB_DT_DEVICE_QUALIFIER),
		uintptr(0),
		uintptr(0),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&transferred)),
	)
	if r0 == 0 {
		return nil, fmt.Errorf("failed to get device qualifier descriptor: %w", e1)
	}

	if transferred < 10 {
		return nil, fmt.Errorf("invalid device qualifier descriptor")
	}

	return &DeviceQualifierDescriptor{
		Length:            buf[0],
		DescriptorType:    buf[1],
		USBVersion:        binary.LittleEndian.Uint16(buf[2:4]),
		DeviceClass:       buf[4],
		DeviceSubClass:    buf[5],
		DeviceProtocol:    buf[6],
		MaxPacketSize0:    buf[7],
		NumConfigurations: buf[8],
		Reserved:          buf[9],
	}, nil
}

// GetCapabilities returns device capabilities (not directly available on Windows)
func (h *DeviceHandle) GetCapabilities() (uint32, error) {
	// Windows doesn't have direct equivalent to Linux usbfs capabilities
	return 0, ErrNotSupported
}

// GetSpeed returns the device speed
func (h *DeviceHandle) GetSpeed() (Speed, error) {
	speed, err := h.Speed()
	if err != nil {
		return SpeedUnknown, err
	}

	// Convert WinUSB speed to library Speed type
	switch speed {
	case LowSpeed:
		return SpeedLow, nil
	case FullSpeed:
		return SpeedFull, nil
	case HighSpeed:
		return SpeedHigh, nil
	case SuperSpeed:
		return SpeedSuper, nil
	default:
		return SpeedUnknown, nil
	}
}

// enumerateRootHubDevnodes returns the devnode of every root hub by walking
// from each USB host controller to its first child.
//
// Each USB host controller has exactly one direct child devnode in the device
// tree: the root hub. Calling CM_Get_Child on the host controller devnode
// therefore gives the root hub's devnode without needing to parse paths.
func enumerateRootHubDevnodes() []uint32 {
	hcDevs, err := enumerateWithGUID(&GUID_DEVINTERFACE_USB_HOST_CONTROLLER)
	if err != nil {
		return nil
	}

	seen := make(map[uint32]bool)
	var result []uint32
	for _, hcDev := range hcDevs {
		child, err := cmGetChild(hcDev.DevInst)
		if err != nil {
			continue
		}
		// Verify the child is actually a hub (has a USB hub interface).
		if _, err := hubInterfacePath(child); err != nil {
			continue
		}
		if !seen[child] {
			seen[child] = true
			result = append(result, child)
		}
	}
	return result
}

// describeRootHub builds a Device for a root hub devnode. It always sets
// Address=1 and DeviceClass=9 (Hub) so that lsusb -t can identify it as the
// root of its bus tree.
func describeRootHub(devInst uint32, bus uint8) *Device {
	id, err := cmGetDeviceID(devInst)
	if err != nil {
		return nil
	}

	paths, err := windows.CM_Get_Device_Interface_List(id, &GUID_DEVINTERFACE_USB_HUB, 0)
	if err != nil || len(paths) == 0 {
		return nil
	}
	hubPath := paths[0]

	var vid, pid uint16
	var usbVersion uint16 = 0x0200 // default to USB 2.0

	if hwID, err := cmGetHardwareID(devInst); err == nil {
		vid, pid = parseVidPidFromHardwareID(hwID)
		// Infer USB version from the hub type substring in the hardware ID.
		// ROOT_HUB30 / ROOT_HUB31 → 3.x, ROOT_HUB20 → 2.0, ROOT_HUB → 1.1.
		hwUpper := strings.ToUpper(hwID)
		if strings.Contains(hwUpper, "ROOT_HUB3") {
			usbVersion = 0x0300
		} else if strings.Contains(hwUpper, "ROOT_HUB2") {
			usbVersion = 0x0200
		} else if strings.Contains(hwUpper, "ROOT_HUB") {
			usbVersion = 0x0110
		}
	}

	dev := &Device{
		Path:       hubPath,
		devicePath: hubPath,
		Bus:        bus,
		Address:    1,
		devInst:    devInst,
		Descriptor: DeviceDescriptor{
			VendorID:    vid,
			ProductID:   pid,
			DeviceClass: 9, // Hub
			USBVersion:  usbVersion,
		},
	}
	return dev
}

// parseVidPidFromHardwareID parses VID and PID from a Windows hardware ID
// string. Hardware IDs for USB root hubs use the format "&VIDxxxx&PIDyyyy"
// (no underscore, four uppercase hex digits), unlike the device-path format
// handled by parseVidPidFromPath.
func parseVidPidFromHardwareID(hwID string) (vid, pid uint16) {
	upper := strings.ToUpper(hwID)

	vidIdx := strings.Index(upper, "&VID")
	if vidIdx >= 0 && vidIdx+8 <= len(upper) {
		if v, err := parseHex4(upper[vidIdx+4 : vidIdx+8]); err == nil {
			vid = v
		}
	}

	pidIdx := strings.Index(upper, "&PID")
	if pidIdx >= 0 && pidIdx+8 <= len(upper) {
		if p, err := parseHex4(upper[pidIdx+4 : pidIdx+8]); err == nil {
			pid = p
		}
	}

	return
}

// GetStatus gets device/interface/endpoint status
func (h *DeviceHandle) GetStatus(recipient, index uint16) (uint16, error) {
	buf := make([]byte, 2)
	requestType := uint8(0x80 | (recipient & 0x1F))

	_, err := h.ControlTransfer(requestType, USB_REQ_GET_STATUS, 0, index, buf, 5000*1000000)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint16(buf), nil
}
