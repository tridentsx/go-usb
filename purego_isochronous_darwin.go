//go:build darwin && !cgo

// Isochronous transfers via IOKit, without cgo.
//
// Isochronous I/O is inherently asynchronous -- ReadIsochPipeAsync and
// WriteIsochPipeAsync submit a run of frames against a future bus frame number
// and return immediately, long before the data has actually moved -- so this
// is the first real asynchronous transfer path in the purego backend, and the
// first place the buffer-lifetime concern flagged when interface claiming
// landed is actually load-bearing.
//
// That concern, precisely: between a submit call returning and its completion
// callback firing, the transfer's buffer and frame list must stay reachable,
// or Go's garbage collector is free to reclaim them while IOKit still holds
// raw pointers to them. Go's GC does not move heap objects, so a stable
// address is not the issue; going out of scope is. Each IOUSBInterfaceInterface
// keeps a pending map from a submitted frame list's address to its
// *IsochronousTransfer for exactly this reason -- as long as that map holds
// the transfer, the transfer holds its own buffer and frame list, and the map
// itself is reachable from the open interface for as long as it is open. No
// runtime.Pinner or cgo pointer-passing rule is needed; a live Go reference is
// sufficient, and the map already is one.
//
// The completion callback itself is shared across every transfer submitted on
// an interface, for a related reason: purego.NewCallback's allocation is
// permanent for the life of the process ("any memory allocated for these
// callbacks is never released"), and there is a hard ceiling on how many
// exist at once. A callback per Submit call would exhaust that ceiling under
// any real streaming workload (a UVC camera submits dozens of these a
// second). IOKit documents arg0 of the IOAsyncCallback1 as the frameList
// pointer the call was given, which is exactly the correlation key the shared
// callback needs to find its way back to the right transfer in the pending
// map.
//
// See issue #14.

package usb

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// IsochronousTransfer represents an isochronous USB transfer.
type IsochronousTransfer struct {
	handle         *DeviceHandle
	endpoint       uint8
	packetSize     int
	numPackets     int
	buffer         []byte
	status         TransferStatus
	actualLength   int
	callback       func(*IsochronousTransfer)
	userData       interface{}
	submitted      bool
	completed      bool
	mutex          sync.Mutex
	packetLengths  []int
	packetStatuses []int

	intf      *IOUSBInterfaceInterface
	frameList []ioUSBIsocFrame
	done      chan struct{}
}

// NewIsochronousTransfer creates a new isochronous transfer.
//
// Deprecated: use DeviceHandle.NewIsochronousTransfer, which is the form used on
// every platform and reports errors.
func NewIsochronousTransfer(handle *DeviceHandle, endpoint uint8, numPackets int, packetSize int) *IsochronousTransfer {
	return &IsochronousTransfer{
		handle:         handle,
		endpoint:       endpoint,
		packetSize:     packetSize,
		numPackets:     numPackets,
		buffer:         make([]byte, numPackets*packetSize),
		packetLengths:  make([]int, numPackets),
		packetStatuses: make([]int, numPackets),
		status:         TransferError,
	}
}

// SetCallback sets the completion callback.
func (t *IsochronousTransfer) SetCallback(callback func(*IsochronousTransfer)) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.callback = callback
}

// SetUserData attaches caller data to the transfer.
func (t *IsochronousTransfer) SetUserData(data interface{}) { t.userData = data }

// GetUserData returns the data attached with SetUserData.
func (t *IsochronousTransfer) GetUserData() interface{} { return t.userData }

// SetPacketLength sets the requested length of one packet.
func (t *IsochronousTransfer) SetPacketLength(packet int, length int) error {
	if packet < 0 || packet >= t.numPackets {
		return ErrInvalidParameter
	}
	if length < 0 || length > t.packetSize {
		return ErrInvalidParameter
	}
	t.packetLengths[packet] = length
	return nil
}

// Submit queues the transfer against a near-future bus frame and returns
// immediately; the transfer is not complete when this returns; call Wait.
func (t *IsochronousTransfer) Submit() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.submitted {
		return fmt.Errorf("transfer already submitted")
	}
	if t.handle == nil || t.handle.closed {
		return ErrDeviceNotFound
	}

	// Endpoint is not tracked to a specific claimed interface, the same
	// simplification ClearHalt and bulkTransfer already make.
	var intf *IOUSBInterfaceInterface
	for _, i := range t.handle.interfaces {
		intf = i
		break
	}
	if intf == nil {
		return fmt.Errorf("no interface claimed for endpoint %02x", t.endpoint)
	}

	if err := intf.ensureAsyncPump(); err != nil {
		return err
	}

	frame, err := intf.GetBusFrameNumber()
	if err != nil {
		return err
	}
	// A few frames in the future, the same margin the cgo backend used, so
	// the request is queued before the bus schedule reaches it.
	startFrame := frame + 10

	t.frameList = make([]ioUSBIsocFrame, t.numPackets)
	for i := range t.frameList {
		length := t.packetLengths[i]
		if length == 0 {
			length = t.packetSize
		}
		t.frameList[i].frReqCount = uint16(length)
	}
	t.done = make(chan struct{})

	key := isocFrameListKey(t.frameList)
	intf.pendingMu.Lock()
	intf.pending[key] = t
	intf.pendingMu.Unlock()

	pipeRef := t.endpoint & 0x0F
	var submitErr error
	if t.endpoint&0x80 != 0 {
		submitErr = intf.readIsochPipeAsync(pipeRef, t.buffer, startFrame, uint32(t.numPackets), &t.frameList[0], intf.callback)
	} else {
		submitErr = intf.writeIsochPipeAsync(pipeRef, t.buffer, startFrame, uint32(t.numPackets), &t.frameList[0], intf.callback)
	}
	if submitErr != nil {
		intf.pendingMu.Lock()
		delete(intf.pending, key)
		intf.pendingMu.Unlock()
		return submitErr
	}

	t.intf = intf
	t.submitted = true
	return nil
}

// Cancel aborts the pipe this transfer is on, which is the only cancellation
// IOKit offers -- there is no way to cancel one specific in-flight request
// without affecting others queued on the same pipe. The aborted transfer
// still completes normally through the callback, now with an error status,
// so Cancel does not itself mark the transfer completed or close done.
func (t *IsochronousTransfer) Cancel() error {
	t.mutex.Lock()
	if !t.submitted {
		t.mutex.Unlock()
		return fmt.Errorf("transfer not submitted")
	}
	if t.completed {
		t.mutex.Unlock()
		return nil
	}
	intf := t.intf
	pipeRef := t.endpoint & 0x0F
	t.mutex.Unlock()

	return intf.AbortPipe(pipeRef)
}

// Wait blocks until the transfer completes.
func (t *IsochronousTransfer) Wait() error {
	t.mutex.Lock()
	if !t.submitted {
		t.mutex.Unlock()
		return fmt.Errorf("transfer not submitted")
	}
	done := t.done
	t.mutex.Unlock()

	<-done
	return nil
}

// Status returns the transfer status.
func (t *IsochronousTransfer) Status() TransferStatus {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.status
}

// ActualLength returns the total bytes transferred.
func (t *IsochronousTransfer) ActualLength() int {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.actualLength
}

// GetPacketData returns the data for a specific packet.
//
// Deprecated: use IsoPacketBuffer.
func (t *IsochronousTransfer) GetPacketData(packet int) ([]byte, error) {
	return t.IsoPacketBuffer(packet)
}

// GetPacketStatus returns the status of a specific packet.
//
// Deprecated: use Packets and read IsoPacketDescriptor.Status.
func (t *IsochronousTransfer) GetPacketStatus(packet int) (int, error) {
	if packet < 0 || packet >= t.numPackets {
		return 0, ErrInvalidParameter
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.packetStatuses[packet], nil
}

// GetPacketActualLength returns the actual length transferred for a packet.
//
// Deprecated: use Packets and read IsoPacketDescriptor.ActualLength.
func (t *IsochronousTransfer) GetPacketActualLength(packet int) (int, error) {
	if packet < 0 || packet >= t.numPackets {
		return 0, ErrInvalidParameter
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.packetLengths[packet], nil
}

// IsochronousTransferIn creates an isochronous transfer for reading and
// submits it.
func (h *DeviceHandle) IsochronousTransferIn(endpoint uint8, numPackets, packetSize int) (*IsochronousTransfer, error) {
	t := NewIsochronousTransfer(h, endpoint|0x80, numPackets, packetSize)
	if err := t.Submit(); err != nil {
		return nil, err
	}
	return t, nil
}

// IsochronousTransferOut creates an isochronous transfer for writing and
// submits it.
func (h *DeviceHandle) IsochronousTransferOut(endpoint uint8, data []byte, numPackets, packetSize int) (*IsochronousTransfer, error) {
	t := NewIsochronousTransfer(h, endpoint&0x7F, numPackets, packetSize)
	copy(t.buffer, data)
	if err := t.Submit(); err != nil {
		return nil, err
	}
	return t, nil
}

// completeAsync records one completion, delivered by the interface's shared
// callback trampoline, and wakes anything blocked in Wait.
func (t *IsochronousTransfer) completeAsync(result int32) {
	t.mutex.Lock()

	t.actualLength = 0
	allSuccess := result == kernSuccess
	for i := range t.frameList {
		t.packetStatuses[i] = int(t.frameList[i].frStatus)
		n := int(t.frameList[i].frActCount)
		t.packetLengths[i] = n
		t.actualLength += n
		if t.frameList[i].frStatus != kernSuccess {
			allSuccess = false
		}
	}
	if allSuccess {
		t.status = TransferCompleted
	} else {
		t.status = TransferError
	}
	t.completed = true
	callback := t.callback
	close(t.done)

	t.mutex.Unlock()

	if callback != nil {
		callback(t)
	}
}

// isocFrameListKey is the pending-map key for a transfer's frame list: the
// address of its first element, which is exactly what IOKit hands back as
// arg0 on completion.
func isocFrameListKey(frameList []ioUSBIsocFrame) uintptr {
	if len(frameList) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&frameList[0]))
}

// --- per-interface async pump -------------------------------------------------

// ensureAsyncPump starts the interface's async event pump on first use. It is
// idempotent and safe to call from multiple goroutines submitting transfers
// concurrently on the same interface.
func (i *IOUSBInterfaceInterface) ensureAsyncPump() error {
	i.asyncMu.Lock()
	if i.asyncStarted {
		err := i.asyncErr
		i.asyncMu.Unlock()
		return err
	}
	i.asyncStarted = true
	i.asyncMu.Unlock()

	k, err := loadIOKit()
	if err != nil {
		i.asyncMu.Lock()
		i.asyncErr = err
		i.asyncMu.Unlock()
		return err
	}

	source, err := i.CreateInterfaceAsyncEventSource()
	if err != nil {
		i.asyncMu.Lock()
		i.asyncErr = err
		i.asyncMu.Unlock()
		return err
	}

	// See the note on kCFRunLoopDefaultMode in purego_hotplug_darwin.go: an
	// equal CFString built locally works exactly like the real constant would,
	// since CFRunLoop matches modes by CFEqual, and avoids dereferencing a
	// foreign global's address through Dlsym.
	mode := k.cfStringRef("kCFRunLoopDefaultMode")
	if mode == 0 {
		i.asyncMu.Lock()
		i.asyncErr = ErrOther
		i.asyncMu.Unlock()
		return ErrOther
	}

	i.pending = make(map[uintptr]*IsochronousTransfer)
	i.callback = purego.NewCallback(func(refCon uintptr, result int32, arg0 uintptr) {
		i.pendingMu.Lock()
		t, ok := i.pending[arg0]
		if ok {
			delete(i.pending, arg0)
		}
		i.pendingMu.Unlock()
		if ok {
			t.completeAsync(result)
		}
	})

	i.asyncReady = make(chan struct{})
	i.asyncDone = make(chan struct{})
	i.asyncMode = mode

	go func() {
		runtime.LockOSThread()
		// Deliberately never unlocked; see the identical note in
		// purego_hotplug_darwin.go's pump goroutine.

		rl := hotplug.CFRunLoopGetCurrent()
		hotplug.CFRunLoopAddSource(rl, source, mode)

		i.asyncMu.Lock()
		i.runLoop = rl
		i.asyncMu.Unlock()
		close(i.asyncReady)

		for {
			i.asyncMu.Lock()
			stopped := i.asyncStopped
			i.asyncMu.Unlock()
			if stopped {
				break
			}
			hotplug.CFRunLoopRunInMode(mode, 3600, true)
		}

		k.CFRelease(mode)
		close(i.asyncDone)
	}()

	return nil
}

// stopAsyncPump stops the pump started by ensureAsyncPump, if one was, and
// blocks until it has exited. Called from release, so it runs whether the
// interface is released explicitly or as part of closing the device handle.
func (i *IOUSBInterfaceInterface) stopAsyncPump() {
	i.asyncMu.Lock()
	if !i.asyncStarted || i.asyncStopped {
		i.asyncMu.Unlock()
		return
	}
	i.asyncStopped = true
	ready := i.asyncReady
	i.asyncMu.Unlock()

	if ready == nil {
		return
	}
	<-ready
	i.asyncMu.Lock()
	rl := i.runLoop
	i.asyncMu.Unlock()
	if rl != 0 {
		hotplug.CFRunLoopStop(rl)
	}
	<-i.asyncDone
}
