//go:build darwin && !cgo

package usb

import (
	"testing"
)

// TestDeviceInterfaceAgreesWithRegistry validates the IOUSBDeviceInterface
// method table against an external oracle.
//
// The IOKit registry already reports each device's vendor, product, release
// number, configuration count and locationID. Calling the corresponding methods
// through the table and comparing gives five independent checks that the offsets
// are right, without trusting the assumption that produced them.
//
// This is the check that a hand-transcribed offset table needs. A wrong offset
// in this neighbourhood lands on another getter — the surrounding entries are all
// simple accessors — so the symptom is a plausible-looking wrong number rather
// than a crash, which is exactly the kind of error that survives review.
//
// It skips when no device offers a USB user client, which is the case on CI
// runners whose only USB devices are Apple's synthetic HID ones.
func TestDeviceInterfaceAgreesWithRegistry(t *testing.T) {
	k, err := loadIOKit()
	if err != nil {
		t.Skipf("IOKit unavailable: %v", err)
	}

	devices, err := enumerateDevices()
	if err != nil {
		t.Fatalf("enumerateDevices: %v", err)
	}
	if len(devices) == 0 {
		t.Skip("no USB devices attached")
	}

	checked := 0

	for _, class := range usbDeviceClasses {
		matching := k.IOServiceMatching(class + "\x00")
		if matching == 0 {
			continue
		}
		var iter uint32
		if ret := k.IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iter); ret != kernSuccess {
			continue
		}

		for {
			service := k.IOIteratorNext(iter)
			if service == 0 {
				break
			}

			// The registry values, which are the oracle.
			var props uintptr
			if r := k.IORegistryEntryCreateCFProperties(service, &props, 0, 0); r != kernSuccess || props == 0 {
				k.IOObjectRelease(service)
				continue
			}
			wantVendor, haveVendor := k.propertyNumber(props, propVendorID)
			wantProduct, _ := k.propertyNumber(props, propProductID)
			wantRelease, haveRelease := k.propertyNumber(props, propDeviceRelease)
			wantConfigs, haveConfigs := k.propertyNumber(props, propNumConfigurations)
			wantLocation, haveLocation := k.propertyNumber(props, propLocationID)
			k.CFRelease(props)

			if !haveVendor {
				k.IOObjectRelease(service)
				continue
			}

			dev, err := openDeviceInterface(service)
			if err != nil {
				// No user client for this device; not a table problem.
				k.IOObjectRelease(service)
				continue
			}

			label := "device"
			if got, err := dev.VendorID(); err != nil {
				t.Errorf("%s: VendorID: %v", label, err)
			} else if uint64(got) != wantVendor {
				t.Errorf("VendorID = %#04x, registry says %#04x; GetDeviceVendor offset is wrong", got, wantVendor)
			} else {
				label = "device " + canonicalHex16(got)
				checked++
			}

			if got, err := dev.ProductID(); err != nil {
				t.Errorf("%s: ProductID: %v", label, err)
			} else if uint64(got) != wantProduct {
				t.Errorf("%s: ProductID = %#04x, registry says %#04x", label, got, wantProduct)
			}

			if haveRelease {
				if got, err := dev.ReleaseNumber(); err != nil {
					t.Errorf("%s: ReleaseNumber: %v", label, err)
				} else if uint64(got) != wantRelease {
					t.Errorf("%s: ReleaseNumber = %#04x, registry says %#04x", label, got, wantRelease)
				}
			}

			if haveConfigs {
				if got, err := dev.NumberOfConfigurations(); err != nil {
					t.Errorf("%s: NumberOfConfigurations: %v", label, err)
				} else if uint64(got) != wantConfigs {
					t.Errorf("%s: NumberOfConfigurations = %d, registry says %d", label, got, wantConfigs)
				}
			}

			if haveLocation {
				if got, err := dev.LocationID(); err != nil {
					t.Errorf("%s: LocationID: %v", label, err)
				} else if uint64(got) != wantLocation {
					t.Errorf("%s: LocationID = %#08x, registry says %#08x", label, got, wantLocation)
				}
			}

			// These have no registry counterpart to compare against, so they are
			// only logged: a wrong offset here would show up as a nonsense value.
			class, _ := dev.DeviceClass()
			subClass, _ := dev.DeviceSubClass()
			protocol, _ := dev.DeviceProtocol()
			speed, _ := dev.DeviceSpeed()
			address, _ := dev.DeviceAddress()
			t.Logf("%s: class=%#02x/%#02x/%#02x speed=%d(%v) address=%d",
				label, class, subClass, protocol, speed, iokitSpeedToSpeed(speed), address)

			dev.release()
			k.IOObjectRelease(service)
		}
		k.IOObjectRelease(iter)
	}

	if checked == 0 {
		t.Skip("no device offered a USB user client; method table not exercised")
	}
	t.Logf("method table validated against the registry for %d device interfaces", checked)
}

// canonicalHex16 renders a 16-bit value as four lowercase hex digits.
func canonicalHex16(v uint16) string {
	const hex = "0123456789abcdef"
	return string([]byte{hex[v>>12&0xf], hex[v>>8&0xf], hex[v>>4&0xf], hex[v&0xf]})
}
