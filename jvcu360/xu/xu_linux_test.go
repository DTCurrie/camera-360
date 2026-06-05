//go:build linux

package xu

import (
	"testing"
	"unsafe"
)

// The Go ctrlQuery struct must be exactly 16 bytes to match the kernel's
// (naturally aligned, non-packed) struct uvc_xu_control_query on LP64. If this
// drifts, the derived ioctl number's size field is wrong and the kernel rejects
// the call.
func TestCtrlQuerySize(t *testing.T) {
	if got := unsafe.Sizeof(ctrlQuery{}); got != 16 {
		t.Errorf("sizeof(ctrlQuery) = %d, want 16", got)
	}
}

func TestUVCIOCCtrlQueryNumber(t *testing.T) {
	if uvcIOCCtrlQuery != 0xc0107521 {
		t.Errorf("uvcIOCCtrlQuery = %#x, want 0xc0107521", uvcIOCCtrlQuery)
	}
}
