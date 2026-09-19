package usb

// Linux hotplug via a NETLINK_KOBJECT_UEVENT socket bound to the kernel's
// own uevent multicast group (group 1) -- the same raw uevent stream udevd
// itself listens to, reached directly with no dependency on udevd or
// libudev running. The kernel sends one of these for every driver-model
// device add/remove, not just USB ones, so every message is filtered on
// SUBSYSTEM=usb and DEVTYPE=usb_device before anything else happens with
// it: a composite device's per-interface children also generate uevents
// (DEVTYPE=usb_interface), which are deliberately not device arrivals in
// their own right.
//
// A uevent's payload is not wrapped in a netlink message header the way
// NETLINK_ROUTE traffic is; it is the raw bytes systemd/udevd's own
// documentation describes: a "ACTION@DEVPATH" header line, NUL-terminated,
// followed by a NUL-terminated KEY=VALUE line per environment variable the
// kernel attached (ACTION, DEVPATH, SUBSYSTEM, DEVTYPE, BUSNUM, DEVNUM,
// ... -- see kobject_uevent_env() in the kernel source). The header line is
// redundant with the ACTION= and DEVPATH= fields also present and is
// skipped rather than parsed a second way.
//
// Arrival is resolved by BUSNUM/DEVNUM back to a real *Device via a fresh
// DeviceList() call, reusing this package's already-correct sysfs
// enumeration rather than hand-building one from the uevent's own fields.
// This relies on the kernel's driver-model ordering: device_add() populates
// sysfs before it calls kobject_uevent(), so the device is already visible
// there by the time this reads the ADD event -- a hard kernel guarantee,
// not a race condition papered over with a sleep, though one short retry
// covers the case where that assumption turns out to be wrong on some
// kernel version this hasn't been checked against yet.
//
// Removal cannot re-query sysfs -- device_del() sends the uevent before
// (or concurrently with) removing the sysfs entry, and either way the
// entry may already be gone. It instead looks up the same bus:addr key in
// a seen map this handle populated when the device arrived, exactly
// mirroring hotplug_windows.go's identical pattern for the identical
// reason: a departing device cannot be described directly, only recalled.

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// linuxUeventGroupKernel is NETLINK_KOBJECT_UEVENT's "kernel" multicast
// group: the raw, unprocessed uevent stream, as opposed to group 2 (the
// "udev" group, carrying events udevd has already tagged with additional
// properties after running its rules -- not used here, so this does not
// depend on udevd being installed or running at all).
const linuxUeventGroupKernel = 1

// linuxHotplugHandle is the HotplugHandle returned to callers.
type linuxHotplugHandle struct {
	mu      sync.Mutex
	stopped bool
	fd      int

	// wakeR/wakeW are a pipe used solely to interrupt pump's epoll_wait
	// immediately when Deregister sets h.stopped -- see pump's own comment
	// for why closing the socket alone (this package's first attempt at
	// this) is not a reliable way to do that.
	wakeR, wakeW int

	seen                map[string]*Device // "bus:addr" -> Device built on arrival
	vendorID, productID uint16
	callback            HotplugCallback

	done chan struct{} // closed when the pump goroutine exits
}

// Deregister stops the watch and blocks until the pump goroutine has
// exited. Writing to wakeW interrupts pump's epoll_wait immediately; it
// has no other way to notice h.stopped once blocked there.
func (h *linuxHotplugHandle) Deregister() error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	h.stopped = true
	h.mu.Unlock()

	syscall.Write(h.wakeW, []byte{0})
	<-h.done
	return nil
}

func (h *linuxHotplugHandle) matches(dev *Device) bool {
	if h.vendorID != 0 && dev.Descriptor.VendorID != h.vendorID {
		return false
	}
	if h.productID != 0 && dev.Descriptor.ProductID != h.productID {
		return false
	}
	return true
}

func linuxHotplugSeenKey(bus, addr uint8) string {
	return fmt.Sprintf("%d:%d", bus, addr)
}

// parseUevent splits a raw kobject uevent payload into its KEY=VALUE
// fields. The leading "ACTION@DEVPATH" header line has no '=' in its
// ACTION half and is silently skipped by the same rule that skips any
// other malformed token, rather than needing its own special case.
func parseUevent(raw []byte) map[string]string {
	fields := make(map[string]string)
	for _, tok := range strings.Split(string(raw), "\x00") {
		if tok == "" {
			continue
		}
		eq := strings.IndexByte(tok, '=')
		if eq < 0 {
			continue
		}
		fields[tok[:eq]] = tok[eq+1:]
	}
	return fields
}

func registerHotplugCallback(vendorID, productID uint16, callback HotplugCallback) (HotplugHandle, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_NETLINK, NETLINK_KOBJECT_UEVENT): %w", err)
	}

	sa := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: linuxUeventGroupKernel}
	if err := syscall.Bind(fd, sa); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind(AF_NETLINK): %w", err)
	}

	var wakeFDs [2]int
	if err := syscall.Pipe2(wakeFDs[:], syscall.O_CLOEXEC|syscall.O_NONBLOCK); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("pipe2: %w", err)
	}

	h := &linuxHotplugHandle{
		fd:        fd,
		wakeR:     wakeFDs[0],
		wakeW:     wakeFDs[1],
		seen:      make(map[string]*Device),
		vendorID:  vendorID,
		productID: productID,
		callback:  callback,
		done:      make(chan struct{}),
	}

	// The socket is already bound, so anything that arrives from here on
	// queues a uevent the pump goroutine will see once it starts reading --
	// nothing in this snapshot can race with it. Matches
	// hotplug_windows.go's identical ordering and its own comment on why.
	if devices, err := DeviceList(); err == nil {
		for _, dev := range devices {
			h.seen[linuxHotplugSeenKey(dev.Bus, dev.Address)] = dev
			if h.matches(dev) {
				callback(HotplugEventArrived, dev)
			}
		}
	}

	go h.pump()

	return h, nil
}

// pump reads uevents off the netlink socket and dispatches them.
//
// Blocks in epoll_wait on two fds: the netlink socket, and wakeR, a pipe
// Deregister writes a byte to specifically to interrupt this wait
// immediately. Same shape as device_linux.go's reapLoop and for the same
// reason -- see that function's comment for the full rationale, including
// the real libusb source (events_posix.c) this mirrors and the kernel
// quirk (devio.c's usbdev_poll reporting readiness via EPOLLOUT) that
// applies there but not here: a netlink socket behaves normally, so
// EPOLLIN is the right interest for it.
//
// This replaced an earlier version that bounded each Recvfrom with
// SO_RCVTIMEO and polled h.stopped on that timer instead. That worked but
// added up to the timeout's worth of latency to every uevent, for no
// reason beyond simplicity; blocking in epoll_wait has none.
func (h *linuxHotplugHandle) pump() {
	defer func() {
		syscall.Close(h.fd)
		syscall.Close(h.wakeR)
		syscall.Close(h.wakeW)
		close(h.done)
	}()

	epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return
	}
	defer syscall.Close(epfd)

	sockEvent := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(h.fd)}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, h.fd, &sockEvent); err != nil {
		return
	}
	wakeEvent := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(h.wakeR)}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, h.wakeR, &wakeEvent); err != nil {
		return
	}

	buf := make([]byte, 8192)
	events := make([]syscall.EpollEvent, 2)
	for {
		h.mu.Lock()
		stopped := h.stopped
		h.mu.Unlock()
		if stopped {
			return
		}

		n, err := syscall.EpollWait(epfd, events, -1)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return
		}

		for i := 0; i < n; i++ {
			if events[i].Fd == int32(h.wakeR) {
				var b [8]byte
				syscall.Read(h.wakeR, b[:])
				continue
			}

			// Drain every uevent currently queued on the socket before
			// going back to epoll_wait, same as reapLoop drains every
			// reapable URB per wakeup.
			for {
				nRead, _, err := syscall.Recvfrom(h.fd, buf, syscall.MSG_DONTWAIT)
				if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
					break
				}
				if err != nil {
					return
				}
				h.handleUevent(buf[:nRead])
			}
		}
	}
}

func (h *linuxHotplugHandle) handleUevent(raw []byte) {
	fields := parseUevent(raw)
	if fields["SUBSYSTEM"] != "usb" || fields["DEVTYPE"] != "usb_device" {
		// Not a whole-device event: a per-interface uevent (DEVTYPE=
		// usb_interface) from a composite device, or an unrelated
		// subsystem's event delivered on the same shared multicast group.
		return
	}

	busNum, err1 := strconv.ParseUint(fields["BUSNUM"], 10, 8)
	devNum, err2 := strconv.ParseUint(fields["DEVNUM"], 10, 8)
	if err1 != nil || err2 != nil {
		return
	}
	bus, addr := uint8(busNum), uint8(devNum)
	key := linuxHotplugSeenKey(bus, addr)

	switch fields["ACTION"] {
	case "add":
		dev := h.findDevice(bus, addr)
		if dev == nil {
			return
		}
		h.seen[key] = dev
		if h.matches(dev) {
			h.callback(HotplugEventArrived, dev)
		}

	case "remove":
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

// findDevice looks up a device by bus and address via a fresh DeviceList(),
// retrying once after a short wait. The kernel guarantees sysfs is
// populated before the add uevent is sent, so this should always succeed
// on the first try; the retry is a safety net against a kernel version
// this has not actually been verified against, not a race this depends on
// to be correct.
func (h *linuxHotplugHandle) findDevice(bus, addr uint8) *Device {
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		devices, err := DeviceList()
		if err != nil {
			return nil
		}
		for _, dev := range devices {
			if dev.Bus == bus && dev.Address == addr {
				return dev
			}
		}
	}
	return nil
}
