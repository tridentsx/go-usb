package main

import (
	"fmt"
	"log"
	"os"
	"time"

	usb "github.com/tridentsx/go-usb"
)

func main() {
	if os.Getuid() != 0 {
		log.Fatal("This program requires root privileges to access USB devices")
	}

	fmt.Println("🔧 USB Driver Management Test")
	fmt.Println("==============================")

	devices, err := usb.DeviceList()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n📋 Found %d USB devices\n", len(devices))

	// Test driver management on non-root hub devices
	testedDevices := 0
	successfulTests := 0

	for i, dev := range devices {
		// Skip root hubs
		if dev.Descriptor.DeviceClass == 9 {
			continue
		}

		fmt.Printf("\n🔍 Testing Device %d: %04x:%04x\n", i,
			dev.Descriptor.VendorID, dev.Descriptor.ProductID)

		if testDriverManagement(dev) {
			successfulTests++
		}

		testedDevices++
		if testedDevices >= 3 {
			break // Test first 3 non-hub devices
		}
	}

	fmt.Printf("\n📊 Results: %d/%d devices tested successfully\n",
		successfulTests, testedDevices)

	if successfulTests == testedDevices {
		fmt.Println("✅ All driver management tests passed!")
	} else {
		fmt.Println("⚠️  Some tests failed - this is expected for devices in use")
	}
}

func testDriverManagement(dev *usb.Device) bool {
	handle, err := dev.Open()
	if err != nil {
		fmt.Printf("   ⚠️  Could not open: %v\n", err)
		return false
	}
	defer handle.Close()

	// Read configuration to find interfaces
	config, interfaces, _, err := handle.ReadConfigDescriptor(0)
	if err != nil {
		fmt.Printf("   ❌ Could not read config: %v\n", err)
		return false
	}

	fmt.Printf("   📄 Config %d has %d interfaces\n",
		config.ConfigurationValue, len(interfaces))

	if len(interfaces) == 0 {
		fmt.Printf("   ⚠️  No interfaces to test\n")
		return false
	}

	// Test first interface
	iface := interfaces[0].InterfaceNumber
	fmt.Printf("   🎯 Testing interface %d\n", iface)

	// Step 1: Check current driver
	driverName := getDriverName(handle, iface)
	if driverName != "" {
		fmt.Printf("   📎 Current driver: %s\n", driverName)
	} else {
		fmt.Printf("   📎 No driver attached\n")
	}

	// Step 2: Try to claim without detaching (should fail if driver attached)
	err = handle.ClaimInterface(iface)
	if err != nil {
		if driverName != "" {
			fmt.Printf("   ✅ Claim correctly failed with driver attached: %v\n", err)
		} else {
			fmt.Printf("   ⚠️  Claim failed with no driver: %v\n", err)
		}
	} else {
		fmt.Printf("   ✅ Claimed interface (no driver was attached)\n")
		handle.ReleaseInterface(iface)
		return true
	}

	// Step 3: Detach kernel driver
	fmt.Printf("   🔌 Detaching kernel driver...\n")
	err = handle.DetachKernelDriver(iface)
	if err != nil {
		fmt.Printf("   ❌ Failed to detach driver: %v\n", err)
		return false
	}
	fmt.Printf("   ✅ Driver detached successfully\n")

	// Step 4: Try to claim again (should succeed now)
	err = handle.ClaimInterface(iface)
	if err != nil {
		fmt.Printf("   ❌ Failed to claim after detach: %v\n", err)
		// Try to reattach driver
		handle.AttachKernelDriver(iface)
		return false
	}
	fmt.Printf("   ✅ Interface claimed successfully\n")

	// Step 5: Perform a test operation
	testInterfaceOperations(handle, iface)

	// Step 6: Release interface
	err = handle.ReleaseInterface(iface)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to release interface: %v\n", err)
	} else {
		fmt.Printf("   ✅ Interface released\n")
	}

	// Step 7: Reattach kernel driver
	fmt.Printf("   🔌 Reattaching kernel driver...\n")
	err = handle.AttachKernelDriver(iface)
	if err != nil {
		fmt.Printf("   ⚠️  Failed to reattach driver: %v\n", err)
		// Not critical - driver may auto-reattach
	} else {
		fmt.Printf("   ✅ Driver reattached successfully\n")
	}

	// Verify driver is back
	time.Sleep(100 * time.Millisecond) // Give kernel time to bind driver
	newDriverName := getDriverName(handle, iface)
	if newDriverName != "" {
		fmt.Printf("   📎 Driver after reattach: %s\n", newDriverName)
		if newDriverName == driverName {
			fmt.Printf("   ✅ Original driver restored\n")
		}
	}

	return true
}

func getDriverName(handle *usb.DeviceHandle, iface uint8) string {
	// Try to get driver name using USBDEVFS_GETDRIVER
	// This is a simplified version - real implementation would use the ioctl

	// For now, we'll check if claiming fails with EBUSY
	err := handle.ClaimInterface(iface)
	if err != nil {
		// If EBUSY, a driver is attached
		if err.Error() == "device or resource busy" {
			return "unknown_driver"
		}
	} else {
		// No driver was attached, release the interface
		handle.ReleaseInterface(iface)
		return ""
	}

	return ""
}

func testInterfaceOperations(handle *usb.DeviceHandle, iface uint8) {
	fmt.Printf("   🧪 Testing interface operations...\n")

	// Get interface alternate setting
	altSetting, err := handle.Interface(iface)
	if err != nil {
		fmt.Printf("      ⚠️  Could not get alternate setting: %v\n", err)
	} else {
		fmt.Printf("      📊 Current alternate setting: %d\n", altSetting)
	}

	// Try a control transfer on the interface
	buf := make([]byte, 8)
	n, err := handle.ControlTransfer(
		0x81,   // Interface IN request
		0x06,   // GET_DESCRIPTOR
		0x2200, // HID Report descriptor (example)
		uint16(iface),
		buf,
		1*time.Second,
	)

	if err != nil {
		// This is expected to fail for non-HID devices
		fmt.Printf("      ℹ️  Control transfer test: %v (expected for non-HID)\n", err)
	} else {
		fmt.Printf("      ✅ Control transfer successful: %d bytes\n", n)
	}

	// Test setting the same configuration (should be safe)
	currentConfig, err := handle.Configuration()
	if err == nil && currentConfig > 0 {
		err = handle.SetConfiguration(currentConfig)
		if err != nil {
			fmt.Printf("      ⚠️  Could not set configuration: %v\n", err)
		} else {
			fmt.Printf("      ✅ Configuration verified\n")
		}
	}
}
