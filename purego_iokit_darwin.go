//go:build darwin && !cgo

// This file reaches IOKit without cgo, using purego to load the frameworks and
// call into them. It is the counterpart of iokit_darwin.go, which does the same
// thing through cgo; see issue #14.
//
// Only enumeration is implemented so far. Opening a device requires calling
// methods on IOKit's COM-style interfaces, which is the next stage.

package usb

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Framework paths. These are the stable public locations; dlopen resolves the
// versioned binary inside the bundle.
const (
	frameworkIOKit          = "/System/Library/Frameworks/IOKit.framework/IOKit"
	frameworkCoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

// CoreFoundation constants.
const (
	kCFStringEncodingUTF8 = 0x08000100

	kCFNumberSInt32Type = 3
	kCFNumberSInt64Type = 4
)

// kIOMainPortDefault is 0, which selects the default IOKit port. Passing 0
// avoids having to resolve the symbol, whose name changed between macOS
// releases (kIOMasterPortDefault before Big Sur).
const kIOMainPortDefault = 0

// kernSuccess is KERN_SUCCESS.
const kernSuccess = 0

// iokitFuncs holds the framework entry points, resolved once.
type iokitFuncs struct {
	// IOKit
	IOServiceMatching                 func(name string) uintptr
	IOServiceGetMatchingServices      func(mainPort uint32, matching uintptr, iterator *uint32) int32
	IOIteratorNext                    func(iterator uint32) uint32
	IOObjectRelease                   func(object uint32) int32
	IORegistryEntryCreateCFProperties func(entry uint32, properties *uintptr, allocator uintptr, options uint32) int32

	// CoreFoundation
	CFRelease                 func(cf uintptr)
	CFDictionaryGetValue      func(dict uintptr, key uintptr) uintptr
	CFStringCreateWithCString func(alloc uintptr, cStr string, encoding uint32) uintptr
	CFStringGetCString        func(str uintptr, buffer *byte, bufferSize int32, encoding uint32) bool
	CFNumberGetValue          func(number uintptr, theType int32, valuePtr unsafe.Pointer) bool
	CFGetTypeID               func(cf uintptr) uint64
	CFNumberGetTypeID         func() uint64
	CFStringGetTypeID         func() uint64
}

var (
	iokitOnce sync.Once
	iokit     iokitFuncs
	iokitErr  error
)

// loadIOKit resolves the framework entry points on first use.
//
// Failure is reported rather than panicking, so a caller on a system where the
// frameworks are unavailable gets an error instead of a crash.
func loadIOKit() (*iokitFuncs, error) {
	iokitOnce.Do(func() {
		ioKit, err := purego.Dlopen(frameworkIOKit, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			iokitErr = fmt.Errorf("loading IOKit: %w", err)
			return
		}
		cf, err := purego.Dlopen(frameworkCoreFoundation, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			iokitErr = fmt.Errorf("loading CoreFoundation: %w", err)
			return
		}

		// RegisterLibFunc panics if a symbol is missing, which would be a
		// programming error rather than a runtime condition, but recover anyway
		// so that it surfaces as an error from DeviceList.
		defer func() {
			if r := recover(); r != nil {
				iokitErr = fmt.Errorf("resolving framework symbols: %v", r)
			}
		}()

		purego.RegisterLibFunc(&iokit.IOServiceMatching, ioKit, "IOServiceMatching")
		purego.RegisterLibFunc(&iokit.IOServiceGetMatchingServices, ioKit, "IOServiceGetMatchingServices")
		purego.RegisterLibFunc(&iokit.IOIteratorNext, ioKit, "IOIteratorNext")
		purego.RegisterLibFunc(&iokit.IOObjectRelease, ioKit, "IOObjectRelease")
		purego.RegisterLibFunc(&iokit.IORegistryEntryCreateCFProperties, ioKit, "IORegistryEntryCreateCFProperties")

		purego.RegisterLibFunc(&iokit.CFRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&iokit.CFDictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&iokit.CFStringCreateWithCString, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&iokit.CFStringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&iokit.CFNumberGetValue, cf, "CFNumberGetValue")
		purego.RegisterLibFunc(&iokit.CFGetTypeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&iokit.CFNumberGetTypeID, cf, "CFNumberGetTypeID")
		purego.RegisterLibFunc(&iokit.CFStringGetTypeID, cf, "CFStringGetTypeID")

		// Plug-in entry points, used for COM-style interface access.
		registerPluginFuncs(ioKit, cf)
	})

	if iokitErr != nil {
		return nil, iokitErr
	}
	return &iokit, nil
}

// cfStringRef creates a CFString for a property key. The caller releases it.
//
// Go strings are not null-terminated, so the terminator is added explicitly
// rather than relying on purego copying the string.
func (k *iokitFuncs) cfStringRef(s string) uintptr {
	return k.CFStringCreateWithCString(0, s+"\x00", kCFStringEncodingUTF8)
}

// propertyNumber reads an integer property from a device's property dictionary.
//
// The second return value reports whether the key was present and numeric, so a
// missing property is distinguishable from a genuine zero.
func (k *iokitFuncs) propertyNumber(props uintptr, key string) (uint64, bool) {
	cfKey := k.cfStringRef(key)
	if cfKey == 0 {
		return 0, false
	}
	defer k.CFRelease(cfKey)

	value := k.CFDictionaryGetValue(props, cfKey)
	if value == 0 {
		return 0, false
	}
	if k.CFGetTypeID(value) != k.CFNumberGetTypeID() {
		return 0, false
	}

	var out int64
	if !k.CFNumberGetValue(value, kCFNumberSInt64Type, unsafe.Pointer(&out)) {
		return 0, false
	}
	return uint64(out), true
}

// propertyString reads a string property from a device's property dictionary.
func (k *iokitFuncs) propertyString(props uintptr, key string) string {
	cfKey := k.cfStringRef(key)
	if cfKey == 0 {
		return ""
	}
	defer k.CFRelease(cfKey)

	value := k.CFDictionaryGetValue(props, cfKey)
	if value == 0 {
		return ""
	}
	if k.CFGetTypeID(value) != k.CFStringGetTypeID() {
		return ""
	}

	// USB string descriptors are at most 126 characters, which cannot exceed
	// 504 bytes of UTF-8 including the terminator.
	buf := make([]byte, 512)
	if !k.CFStringGetCString(value, &buf[0], int32(len(buf)), kCFStringEncodingUTF8) {
		return ""
	}

	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// IOKit registry property keys for a USB device.
const (
	propVendorID          = "idVendor"
	propProductID         = "idProduct"
	propDeviceRelease     = "bcdDevice"
	propUSBVersion        = "bcdUSB"
	propDeviceClass       = "bDeviceClass"
	propDeviceSubClass    = "bDeviceSubClass"
	propDeviceProtocol    = "bDeviceProtocol"
	propMaxPacketSize0    = "bMaxPacketSize0"
	propNumConfigurations = "bNumConfigurations"
	propAddress           = "USB Address"
	propLocationID        = "locationID"
	propManufacturerIndex = "iManufacturer"
	propProductIndex      = "iProduct"
	propSerialIndex       = "iSerialNumber"
	propVendorName        = "USB Vendor Name"
	propProductName       = "USB Product Name"
	propSerialNumber      = "USB Serial Number"
)

// IOKit class names for a USB device. IOUSBHostDevice is the modern class;
// IOUSBDevice is matched too so that older systems still enumerate.
var usbDeviceClasses = []string{"IOUSBHostDevice", "IOUSBDevice"}

// enumerateDevices reads every USB device out of the IOKit registry.
//
// Nothing is opened: the properties come from the registry, which is why this
// works regardless of which driver owns a device.
func enumerateDevices() ([]*Device, error) {
	k, err := loadIOKit()
	if err != nil {
		return nil, err
	}

	devices := make([]*Device, 0, 8)
	seen := make(map[uint32]bool)

	for _, class := range usbDeviceClasses {
		matching := k.IOServiceMatching(class + "\x00")
		if matching == 0 {
			continue
		}

		// IOServiceGetMatchingServices consumes the matching dictionary, so it
		// must not be released here.
		var iterator uint32
		if ret := k.IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iterator); ret != kernSuccess {
			continue
		}

		for {
			service := k.IOIteratorNext(iterator)
			if service == 0 {
				break
			}

			if dev, ok := k.deviceFromService(service); ok {
				// A device can match both class names; keep the first.
				if !seen[dev.IOKitDevice.LocationID] {
					seen[dev.IOKitDevice.LocationID] = true
					devices = append(devices, dev)
				}
			}
			k.IOObjectRelease(service)
		}
		k.IOObjectRelease(iterator)
	}

	return devices, nil
}

// deviceFromService builds a Device from one IOKit registry entry.
func (k *iokitFuncs) deviceFromService(service uint32) (*Device, bool) {
	var props uintptr
	if ret := k.IORegistryEntryCreateCFProperties(service, &props, 0, 0); ret != kernSuccess || props == 0 {
		return nil, false
	}
	defer k.CFRelease(props)

	vendorID, haveVendor := k.propertyNumber(props, propVendorID)
	productID, haveProduct := k.propertyNumber(props, propProductID)
	if !haveVendor || !haveProduct {
		// Without a vendor and product ID this is not a USB device we can
		// describe, so skip it rather than reporting zeros.
		return nil, false
	}

	locationID, _ := k.propertyNumber(props, propLocationID)
	address, _ := k.propertyNumber(props, propAddress)

	descriptor := DeviceDescriptor{
		Length:         18,
		DescriptorType: USB_DT_DEVICE,
		VendorID:       uint16(vendorID),
		ProductID:      uint16(productID),
	}
	if v, ok := k.propertyNumber(props, propUSBVersion); ok {
		descriptor.USBVersion = uint16(v)
	}
	if v, ok := k.propertyNumber(props, propDeviceClass); ok {
		descriptor.DeviceClass = uint8(v)
	}
	if v, ok := k.propertyNumber(props, propDeviceSubClass); ok {
		descriptor.DeviceSubClass = uint8(v)
	}
	if v, ok := k.propertyNumber(props, propDeviceProtocol); ok {
		descriptor.DeviceProtocol = uint8(v)
	}
	if v, ok := k.propertyNumber(props, propMaxPacketSize0); ok {
		descriptor.MaxPacketSize0 = uint8(v)
	}
	if v, ok := k.propertyNumber(props, propDeviceRelease); ok {
		descriptor.DeviceVersion = uint16(v)
	}
	if v, ok := k.propertyNumber(props, propNumConfigurations); ok {
		descriptor.NumConfigurations = uint8(v)
	}

	// IOKit normally exposes the resolved strings rather than their descriptor
	// indices; the indices live in the device descriptor, which needs a device
	// interface to read. Some registry entries do publish them, so try, and
	// leave the fields zero when they are absent rather than inventing values.
	if v, ok := k.propertyNumber(props, propManufacturerIndex); ok {
		descriptor.ManufacturerIndex = uint8(v)
	}
	if v, ok := k.propertyNumber(props, propProductIndex); ok {
		descriptor.ProductIndex = uint8(v)
	}
	if v, ok := k.propertyNumber(props, propSerialIndex); ok {
		descriptor.SerialNumberIndex = uint8(v)
	}

	// The strings themselves are cached regardless, since they are available
	// without opening the device.
	strings := &DeviceStrings{
		Manufacturer: k.propertyString(props, propVendorName),
		Product:      k.propertyString(props, propProductName),
		Serial:       k.propertyString(props, propSerialNumber),
	}

	return &Device{
		Path:       iokitDevicePath(uint32(locationID)),
		Bus:        busFromLocationID(uint32(locationID)),
		Address:    uint8(address),
		Descriptor: descriptor,
		IOKitDevice: &IOKitDevice{
			Service:    service,
			LocationID: uint32(locationID),
			VendorID:   uint16(vendorID),
			ProductID:  uint16(productID),
			Bus:        busFromLocationID(uint32(locationID)),
			Address:    uint8(address),
		},
		SysfsStrings:  strings,
		CachedStrings: strings,
	}, true
}
