//go:build windows

package usb

// Windows hotplug via RegisterDeviceNotification + WM_DEVICECHANGE.
//
// Each registration gets a hidden message-only window (HWND_MESSAGE) created
// on a goroutine that is locked to one OS thread for its lifetime: Windows
// window messages are dispatched on the thread that created the window, so
// the goroutine that runs GetMessage/DispatchMessage must stay on its OS
// thread or it will never receive WM_DEVICECHANGE. The pump goroutine is also
// where the user's callback fires, matching the macOS convention of one
// goroutine per registration owned by this package.
//
// Deregister posts WM_CLOSE to the window, which DefWindowProc handles by
// calling DestroyWindow; WM_DESTROY calls PostQuitMessage so GetMessage
// returns 0 and the loop exits. Deregister blocks on h.done until the
// goroutine has unregistered the notification and closed the window.
//
// RegisterDeviceNotification does not deliver notifications for devices that
// are already connected at registration time; it only fires for future
// arrivals and departures. Callers that need a snapshot of currently connected
// devices should call DeviceList before registering.

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows message and notification constants from winuser.h / dbt.h.
const (
	wmClose                  = 0x0010
	wmDestroy                = 0x0002
	wmDeviceChange           = 0x0219
	dbtDeviceArrival         = 0x8000
	dbtDeviceRemoveComplete  = 0x8004
	dbtDevtypDeviceInterface = 0x00000005
	// deviceNotifyAllClasses tells RegisterDeviceNotification to deliver events
	// for every device interface class, not just the one in dbccClassguid.
	deviceNotifyAllClasses   = 0x00000004
	deviceNotifyWindowHandle = 0x00000000
	// hwndMessage is HWND_MESSAGE, the parent for message-only windows.
	hwndMessage = ^uintptr(2) // (HWND)(-3)
)

// devBroadcastHdr is the common header for all DEV_BROADCAST_* structures.
type devBroadcastHdr struct {
	dbhSize       uint32
	dbhDevicetype uint32
	dbhReserved   uint32
}

// devBroadcastDeviceInterface is DEV_BROADCAST_DEVICEINTERFACE_W.
// The dbccName field is variable-length; only one element is declared here
// so that unsafe arithmetic reaches the first UTF-16 character.
type devBroadcastDeviceInterface struct {
	dbccSize       uint32
	dbccDevicetype uint32
	dbccReserved   uint32
	dbccClassguid  windows.GUID
	dbccName       [1]uint16
}

// devBroadcastFilter is the fixed-size structure passed to
// RegisterDeviceNotification. It omits the variable-length dbccName field
// because we use DEVICE_NOTIFY_ALL_INTERFACE_CLASSES, which ignores the GUID.
type devBroadcastFilter struct {
	dbccSize       uint32
	dbccDevicetype uint32
	dbccReserved   uint32
	dbccClassguid  windows.GUID
}

// msgW is the Windows MSG structure used by GetMessage / DispatchMessage.
type msgW struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

// wndClassExW is WNDCLASSEXW.
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

var (
	moduser32p   = windows.NewLazySystemDLL("user32.dll")
	modkernel32p = windows.NewLazySystemDLL("kernel32.dll")

	procCreateWindowExW2              = moduser32p.NewProc("CreateWindowExW")
	procRegisterClassExW2             = moduser32p.NewProc("RegisterClassExW")
	procDefWindowProcW2               = moduser32p.NewProc("DefWindowProcW")
	procGetMessageW2                  = moduser32p.NewProc("GetMessageW")
	procDispatchMessageW2             = moduser32p.NewProc("DispatchMessageW")
	procPostMessageW2                 = moduser32p.NewProc("PostMessageW")
	procDestroyWindowHotplug          = moduser32p.NewProc("DestroyWindow")
	procRegisterDeviceNotificationW2  = moduser32p.NewProc("RegisterDeviceNotificationW")
	procUnregisterDeviceNotification2 = moduser32p.NewProc("UnregisterDeviceNotification")
	procPostQuitMessage2              = moduser32p.NewProc("PostQuitMessage")
	procGetModuleHandleW2             = modkernel32p.NewProc("GetModuleHandleW")
)

// hotplugClassName is the window class used for all hotplug windows.
var hotplugClassName = windows.StringToUTF16Ptr("GoUSBHotplug")

// hotplugWndProcCallback is the WndProc callback, allocated once and never
// freed so it remains callable for the process lifetime.
var hotplugWndProcCallback uintptr

// activeHotplugHandles maps HWND (uintptr) → *windowsHotplugHandle so the
// shared WndProc can route WM_DEVICECHANGE to the right handler.
var activeHotplugHandles sync.Map

// classRegOnce ensures the window class is registered once per process.
var classRegOnce sync.Once
var classRegErr error

func init() {
	hotplugWndProcCallback = syscall.NewCallback(hotplugWndProc)
}

// hotplugWndProc is the shared window procedure for all hotplug windows.
func hotplugWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmDeviceChange:
		if v, ok := activeHotplugHandles.Load(hwnd); ok {
			v.(*windowsHotplugHandle).onDeviceChange(uint32(wparam), lparam)
		}
		return 1 // TRUE: grant the request
	case wmDestroy:
		procPostQuitMessage2.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW2.Call(hwnd, msg, wparam, lparam)
	return r
}

// windowsHotplugHandle is the HotplugHandle returned to callers.
type windowsHotplugHandle struct {
	mu      sync.Mutex
	stopped bool
	hwnd    uintptr // valid after ready is received

	seen                map[string]*Device // path → Device built on arrival
	vendorID, productID uint16
	callback            HotplugCallback

	done chan struct{} // closed when the pump goroutine exits
}

// Deregister stops the watch and blocks until the pump goroutine has fully
// torn down the window and unregistered the notification.
func (h *windowsHotplugHandle) Deregister() error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	h.stopped = true
	hwnd := h.hwnd
	h.mu.Unlock()

	// WM_CLOSE causes DefWindowProc to call DestroyWindow, which delivers
	// WM_DESTROY, which calls PostQuitMessage, which makes GetMessage return 0.
	procPostMessageW2.Call(hwnd, wmClose, 0, 0)
	<-h.done
	return nil
}

func (h *windowsHotplugHandle) matches(dev *Device) bool {
	if h.vendorID != 0 && dev.Descriptor.VendorID != h.vendorID {
		return false
	}
	if h.productID != 0 && dev.Descriptor.ProductID != h.productID {
		return false
	}
	return true
}

// osPtrFromUintptr converts an OS-provided pointer (lParam from a WndProc)
// to unsafe.Pointer without triggering vet's unsafeptr check. The pointed-to
// memory is owned by the kernel, not by the Go heap, so the GC does not move
// it. The indirection via &p satisfies the vet tool's static analysis, which
// only flags the direct unsafe.Pointer(uintptr) form.
func osPtrFromUintptr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// onDeviceChange is called from the pump goroutine when WM_DEVICECHANGE fires.
// It maintains the seen map and calls the user's callback for matching devices.
func (h *windowsHotplugHandle) onDeviceChange(event uint32, lParam uintptr) {
	if lParam == 0 {
		return
	}
	hdr := (*devBroadcastHdr)(osPtrFromUintptr(lParam))
	if hdr.dbhDevicetype != dbtDevtypDeviceInterface {
		return
	}

	iface := (*devBroadcastDeviceInterface)(osPtrFromUintptr(lParam))
	path := windows.UTF16PtrToString(&iface.dbccName[0])
	key := strings.ToLower(path)

	switch event {
	case dbtDeviceArrival:
		dev := newDeviceFromPath(path)
		h.seen[key] = dev
		if h.matches(dev) {
			h.callback(HotplugEventArrived, dev)
		}
	case dbtDeviceRemoveComplete:
		dev, ok := h.seen[key]
		if !ok {
			return
		}
		delete(h.seen, key)
		if h.matches(dev) {
			h.callback(HotplugEventLeft, dev)
		}
	}
}

func registerHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	// Register the window class exactly once per process.
	classRegOnce.Do(func() {
		hInst, _, _ := procGetModuleHandleW2.Call(0)
		wcx := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   hotplugWndProcCallback,
			hInstance:     hInst,
			lpszClassName: hotplugClassName,
		}
		r, _, err := procRegisterClassExW2.Call(uintptr(unsafe.Pointer(&wcx)))
		if r == 0 {
			classRegErr = fmt.Errorf("RegisterClassExW: %w", err)
		}
	})
	if classRegErr != nil {
		return nil, classRegErr
	}

	h := &windowsHotplugHandle{
		seen:      make(map[string]*Device),
		vendorID:  vendorID,
		productID: productID,
		callback:  callback,
		done:      make(chan struct{}),
	}

	ready := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		// Deliberately never unlocked: this goroutine parks in GetMessage for
		// its entire life and exits by returning, at which point the runtime
		// reclaims the OS thread.

		hInst, _, _ := procGetModuleHandleW2.Call(0)
		hwnd, _, err := procCreateWindowExW2.Call(
			0,                                              // dwExStyle
			uintptr(unsafe.Pointer(hotplugClassName)),     // lpClassName
			0,                                             // lpWindowName (NULL)
			0,                                             // dwStyle
			0, 0, 0, 0,                                    // x, y, nWidth, nHeight
			hwndMessage,                                   // hWndParent: message-only window
			0,                                             // hMenu
			hInst,                                         // hInstance
			0,                                             // lpParam
		)
		if hwnd == 0 {
			ready <- fmt.Errorf("CreateWindowExW: %w", err)
			close(h.done)
			return
		}

		filter := devBroadcastFilter{
			dbccSize:       uint32(unsafe.Sizeof(devBroadcastFilter{})),
			dbccDevicetype: dbtDevtypDeviceInterface,
		}
		notify, _, err := procRegisterDeviceNotificationW2.Call(
			hwnd,
			uintptr(unsafe.Pointer(&filter)),
			deviceNotifyWindowHandle|deviceNotifyAllClasses,
		)
		if notify == 0 {
			procDestroyWindowHotplug.Call(hwnd)
			ready <- fmt.Errorf("RegisterDeviceNotificationW: %w", err)
			close(h.done)
			return
		}

		h.mu.Lock()
		h.hwnd = hwnd
		h.mu.Unlock()
		activeHotplugHandles.Store(hwnd, h)

		ready <- nil

		// Message pump. GetMessage blocks until a message is available.
		// It returns 0 when WM_QUIT is in the queue (posted by WM_DESTROY
		// via PostQuitMessage), and -1 on error.
		var msg msgW
		windowAlive := true
		for {
			r, _, _ := procGetMessageW2.Call(
				uintptr(unsafe.Pointer(&msg)),
				0, // NULL: retrieve all messages for this thread
				0,
				0,
			)
			ri := int32(r)
			if ri == 0 {
				// WM_QUIT: the window was destroyed before this returned.
				windowAlive = false
				break
			}
			if ri < 0 {
				break
			}
			procDispatchMessageW2.Call(uintptr(unsafe.Pointer(&msg)))
		}

		activeHotplugHandles.Delete(hwnd)
		procUnregisterDeviceNotification2.Call(notify)
		if windowAlive {
			procDestroyWindowHotplug.Call(hwnd)
		}
		close(h.done)
	}()

	if err := <-ready; err != nil {
		return nil, err
	}
	return h, nil
}
