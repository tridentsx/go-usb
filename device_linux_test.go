package usb

import (
	"syscall"
	"testing"
)

func TestShouldTreatClaimBusyAsSuccess(t *testing.T) {
	t.Run("wrapped fd converts ebusy to success", func(t *testing.T) {
		h := &DeviceHandle{wrapped: true}
		if !h.shouldTreatClaimBusyAsSuccess(syscall.EBUSY) {
			t.Fatalf("expected wrapped handle to treat EBUSY as success")
		}
	})

	t.Run("non wrapped fd keeps ebusy as error", func(t *testing.T) {
		h := &DeviceHandle{wrapped: false}
		if h.shouldTreatClaimBusyAsSuccess(syscall.EBUSY) {
			t.Fatalf("did not expect non-wrapped handle to treat EBUSY as success")
		}
	})

	t.Run("wrapped fd does not swallow other errors", func(t *testing.T) {
		h := &DeviceHandle{wrapped: true}
		if h.shouldTreatClaimBusyAsSuccess(syscall.EINVAL) {
			t.Fatalf("did not expect wrapped handle to treat non-EBUSY as success")
		}
	})
}
