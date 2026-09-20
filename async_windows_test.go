package usb

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// newOverlappedTestFile opens a plain, throwaway file with
// FILE_FLAG_OVERLAPPED so tests can exercise the real IOCP mechanics
// registerAsyncCompletion/iocpPump use, without needing a real USB device:
// CreateIoCompletionPort works with any handle opened for overlapped I/O,
// not specifically a WinUSB one. windows.CreateFile is used directly
// rather than os.CreateTemp, since Go's os package already associates its
// own file handles with the runtime's internal completion port, and a
// handle can only ever be associated with one completion port for its
// whole lifetime.
func newOverlappedTestFile(t *testing.T) windows.Handle {
	t.Helper()

	path := filepath.Join(t.TempDir(), "iocp-test-file")
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}

	fileHandle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	t.Cleanup(func() {
		windows.CloseHandle(fileHandle)
		os.Remove(path)
	})
	return fileHandle
}

// TestIocpPumpDispatchesCompletion is a regression test for the core
// mechanism that replaced the old goroutine-per-Submit design: a real I/O
// completion port shared across every AsyncTransfer on a handle, serviced
// by one pump goroutine, dispatching each completion back to the right
// transfer by its OVERLAPPED's address. PostQueuedCompletionStatus injects
// a synthetic completion exactly like a real completed WinUsb_ReadPipe/
// WritePipe/ControlTransfer call would generate, so this exercises the
// real dispatch path without needing real hardware.
func TestIocpPumpDispatchesCompletion(t *testing.T) {
	h := &DeviceHandle{fileHandle: newOverlappedTestFile(t)}

	transfer := &AsyncTransfer{handle: h}
	transfer.cond = sync.NewCond(&transfer.mu)

	if err := h.registerAsyncCompletion(&transfer.overlapped, transfer); err != nil {
		t.Fatalf("registerAsyncCompletion: %v", err)
	}

	const wantQty = 42
	if err := windows.PostQueuedCompletionStatus(h.iocp, wantQty, 0, &transfer.overlapped); err != nil {
		t.Fatalf("PostQueuedCompletionStatus: %v", err)
	}

	done := make(chan struct{})
	go func() {
		transfer.mu.Lock()
		for !transfer.done {
			transfer.cond.Wait()
		}
		transfer.mu.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("iocpPump did not dispatch the posted completion within 2s")
	}

	transfer.mu.Lock()
	xferred, lastErr := transfer.xferred, transfer.lastErr
	transfer.mu.Unlock()

	if xferred != wantQty {
		t.Errorf("xferred = %d, want %d", xferred, wantQty)
	}
	if lastErr != nil {
		t.Errorf("lastErr = %v, want nil", lastErr)
	}
}

// TestCloseDoesNotDeadlockWithIdlePump is a regression test for the real
// deadlock class found on the Linux side this same session (see
// device_linux.go's reapLoop and hotplug_linux.go's pump): Close must not
// depend on something already outstanding to cancel, or on the pump
// noticing h.closed on its own, when the pump can be sitting in a truly
// blocking wait (here, GetQueuedCompletionStatus(..., INFINITE)) with
// nothing pending at all. Deliberately leaves the registered transfer
// forever incomplete -- the exact state that would have deadlocked the
// pre-fix Windows Close (which never woke or cancelled anything) and the
// pre-fix Linux reapLoop/hotplug pump alike.
func TestCloseDoesNotDeadlockWithIdlePump(t *testing.T) {
	h := &DeviceHandle{
		fileHandle:       newOverlappedTestFile(t),
		interfaceHandles: make(map[uint8]winusbInterfaceHandle),
	}

	transfer := &AsyncTransfer{handle: h}
	transfer.cond = sync.NewCond(&transfer.mu)
	if err := h.registerAsyncCompletion(&transfer.overlapped, transfer); err != nil {
		t.Fatalf("registerAsyncCompletion: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- h.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s -- iocpPump deadlock")
	}
}

// TestCloseDoesNotOrphanARacingRealCompletion is a regression test for a
// real deadlock found on real hardware (a go-usb-jig board): Close cancels
// every pending transfer via CancelIoEx, then immediately posts a synthetic
// nil-overlapped completion on the same port to wake iocpPump. CancelIoEx
// only requests cancellation and returns immediately -- Windows documents
// the real completion (ERROR_OPERATION_ABORTED) as arriving later through
// the normal completion mechanism, with no ordering guarantee against that
// synthetic wake. On real hardware the synthetic wake reliably arrived
// first: iocpPump saw the nil overlapped, observed h.closed, and returned
// immediately, before the transfer's own real cancellation completion ever
// arrived -- orphaning it. AsyncTransfer.Wait then blocked forever, since
// nothing remained that would ever call its condition variable's Broadcast.
//
// This reproduces the ordering without needing real hardware or a real
// cancellation: the delayed goroutine below stands in for the real
// completion CancelIoEx would eventually trigger, posted well after Close
// has already had time to post its own synthetic wake.
func TestCloseDoesNotOrphanARacingRealCompletion(t *testing.T) {
	h := &DeviceHandle{
		fileHandle:       newOverlappedTestFile(t),
		interfaceHandles: make(map[uint8]winusbInterfaceHandle),
	}

	transfer := &AsyncTransfer{handle: h}
	transfer.cond = sync.NewCond(&transfer.mu)
	if err := h.registerAsyncCompletion(&transfer.overlapped, transfer); err != nil {
		t.Fatalf("registerAsyncCompletion: %v", err)
	}

	const wantQty = 7
	go func() {
		// Stand-in for CancelIoEx's real, slightly-delayed completion.
		time.Sleep(20 * time.Millisecond)
		if err := windows.PostQueuedCompletionStatus(h.iocp, wantQty, 0, &transfer.overlapped); err != nil {
			t.Errorf("PostQueuedCompletionStatus: %v", err)
		}
	}()

	closeDone := make(chan error, 1)
	go func() { closeDone <- h.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(iocpDrainTimeout + 2*time.Second):
		t.Fatal("Close did not return -- iocpPump stuck")
	}

	waitDone := make(chan struct{})
	go func() {
		transfer.mu.Lock()
		for !transfer.done {
			transfer.cond.Wait()
		}
		transfer.mu.Unlock()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(1 * time.Second):
		t.Fatal("the transfer's completion was never dispatched -- orphaned by Close, the exact bug this test guards against")
	}

	transfer.mu.Lock()
	xferred := transfer.xferred
	transfer.mu.Unlock()
	if xferred != wantQty {
		t.Errorf("xferred = %d, want %d", xferred, wantQty)
	}
}
