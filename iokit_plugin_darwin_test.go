//go:build darwin

package usb

import "testing"

// TestUUIDRoundTripsThroughCoreFoundation asks CoreFoundation what UUID it built
// from our raw bytes and compares it against the canonical text form.
//
// This is the check that catches a byte-order mistake. Passing the sixteen bytes
// in the wrong order produces a valid CFUUID for a *different* UUID, so creation
// succeeds and the error only surfaces later as
// IOCreatePlugInInterfaceForService reporting kIOReturnUnsupported for every
// device — which is exactly what happened before this test existed.
func TestUUIDRoundTripsThroughCoreFoundation(t *testing.T) {
	k, err := loadIOKit()
	if err != nil {
		t.Fatalf("loadIOKit: %v", err)
	}
	if iokitPlugin.CFUUIDCreateFromUUIDBytes == nil || iokitPlugin.CFUUIDCreateString == nil {
		t.Fatal("CFUUID entry points not resolved")
	}

	for name, raw := range allPluginUUIDs() {
		uuid := iokitPlugin.CFUUIDCreateFromUUIDBytes(0, uuidBytes(raw))
		if uuid == 0 {
			t.Errorf("%s: CFUUIDCreateFromUUIDBytes returned NULL", name)
			continue
		}

		str := iokitPlugin.CFUUIDCreateString(0, uuid)
		if str == 0 {
			t.Errorf("%s: CFUUIDCreateString returned NULL", name)
			k.CFRelease(uuid)
			continue
		}

		buf := make([]byte, 64)
		if !k.CFStringGetCString(str, &buf[0], int32(len(buf)), kCFStringEncodingUTF8) {
			t.Errorf("%s: CFStringGetCString failed", name)
		} else {
			got := ""
			for i, b := range buf {
				if b == 0 {
					got = string(buf[:i])
					break
				}
			}
			if want := canonicalUUIDString(raw); got != want {
				t.Errorf("%s: CoreFoundation reports %s, want %s", name, got, want)
			} else {
				t.Logf("%s = %s", name, got)
			}
		}

		k.CFRelease(str)
		k.CFRelease(uuid)
	}
}
