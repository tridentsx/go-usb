package usb

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winusbControlTransfer invokes WinUsb_ControlTransfer with the correct ABI.
//
// The signature is:
//
//	BOOL WinUsb_ControlTransfer(
//	  WINUSB_INTERFACE_HANDLE InterfaceHandle,
//	  WINUSB_SETUP_PACKET     SetupPacket,      // by value, 8 bytes
//	  PUCHAR                  Buffer,
//	  ULONG                   BufferLength,
//	  PULONG                  LengthTransferred,
//	  LPOVERLAPPED            Overlapped);
//
// SetupPacket is passed *by value*. Passing a pointer instead makes the callee
// decode bmRequestType and bRequest from the low bytes of that pointer, which
// fails every transfer with ERROR_GEN_FAILURE. setupPacketArgs expands the
// packet into the right number of machine words for the target architecture.
//
// transferred and overlapped may be nil.
func winusbControlTransfer(
	handle winusbInterfaceHandle,
	packet [setupPacketSize]byte,
	data []byte,
	transferred *uint32,
	overlapped *windows.Overlapped,
) (bool, error) {
	var dataPtr unsafe.Pointer
	if len(data) > 0 {
		dataPtr = unsafe.Pointer(&data[0])
	}

	args := make([]uintptr, 0, 7)
	args = append(args, uintptr(handle))
	args = append(args, setupPacketArgs(packet)...)
	args = append(args,
		uintptr(dataPtr),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(transferred)),
		uintptr(unsafe.Pointer(overlapped)),
	)

	r0, _, e1 := syscall.SyscallN(procWinUsb_ControlTransfer.Addr(), args...)
	if r0 == 0 {
		return false, e1
	}
	return true, nil
}
