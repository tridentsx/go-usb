//go:build darwin && !cgo

package usb

import (
	"testing"
	"unsafe"
)

// TEMPORARY diagnostic, not for commit. Reports where obtaining a device
// interface fails, and with which code, for every device in the registry.
func TestProbeVtableDispatch(t *testing.T) {
	k, err := loadIOKit()
	if err != nil {
		t.Fatalf("loadIOKit: %v", err)
	}

	if iokitPlugin.CFUUIDCreateFromUUIDBytes == nil {
		t.Fatal("CFUUIDCreateFromUUIDBytes not resolved")
	}
	if iokitPlugin.IOCreatePlugInInterfaceForService == nil {
		t.Fatal("IOCreatePlugInInterfaceForService not resolved")
	}

	pluginType := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(kIOUSBDeviceUserClientTypeID))
	interfaceType := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(kIOCFPlugInInterfaceID))
	t.Logf("CFUUIDCreateFromUUIDBytes: pluginType=%#x interfaceType=%#x", pluginType, interfaceType)
	if pluginType == 0 || interfaceType == 0 {
		t.Fatal("CFUUID creation returned NULL; UUID bytes or struct passing is wrong")
	}

	for _, class := range usbDeviceClasses {
		matching := k.IOServiceMatching(class + "\x00")
		if matching == 0 {
			t.Logf("class %s: IOServiceMatching returned NULL", class)
			continue
		}
		var iter uint32
		if ret := k.IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iter); ret != kernSuccess {
			t.Logf("class %s: IOServiceGetMatchingServices ret=%d", class, ret)
			continue
		}

		count := 0
		for {
			service := k.IOIteratorNext(iter)
			if service == 0 {
				break
			}
			count++

			var props uintptr
			vid, pid := uint64(0), uint64(0)
			if r := k.IORegistryEntryCreateCFProperties(service, &props, 0, 0); r == kernSuccess && props != 0 {
				vid, _ = k.propertyNumber(props, propVendorID)
				pid, _ = k.propertyNumber(props, propProductID)
				k.CFRelease(props)
			}

			var plugin unsafe.Pointer
			var score int32
			ret := iokitPlugin.IOCreatePlugInInterfaceForService(service, pluginType, interfaceType, &plugin, &score)
			if ret != kernSuccess || plugin == nil {
				t.Logf("  %04x:%04x IOCreatePlugInInterfaceForService ret=%#x (%d) plugin=%p score=%d",
					vid, pid, uint32(ret), ret, plugin, score)
				k.IOObjectRelease(service)
				continue
			}

			vtable := vtableOf(plugin)
			t.Logf("  %04x:%04x plugin=%p vtable=%p QueryInterface=%#x Release=%#x version=%d revision=%d",
				vid, pid, plugin, vtable, vtable.QueryInterface, vtable.Release, vtable.version, vtable.revision)

			di, derr := deviceInterfaceForService(service)
			if derr != nil {
				t.Logf("  %04x:%04x deviceInterfaceForService: %v", vid, pid, derr)
			} else {
				t.Logf("  %04x:%04x GOT DEVICE INTERFACE %p", vid, pid, di)
				releaseDeviceInterface(di)
			}
			k.IOObjectRelease(service)
		}
		t.Logf("class %s: %d devices", class, count)
		k.IOObjectRelease(iter)
	}
}
