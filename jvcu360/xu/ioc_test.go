package xu

import "testing"

// The encoded UVCIOC_CTRL_QUERY number must be exactly _IOWR('u', 0x21, 16) for
// the 16-byte struct uvc_xu_control_query. This pins the arithmetic so a wrong
// magic number (a classic copy-paste bug — e.g. the often-cited but wrong
// 0xc0185524, which assumes a 24-byte struct and type 'U'/0x24) is caught off
// the device, before it turns into an ENOTTY at runtime.
func TestIOWR_UVCCtrlQueryNumber(t *testing.T) {
	const want = 0xc0107521
	if got := iowr('u', 0x21, 16); got != want {
		t.Errorf("iowr('u', 0x21, 16) = %#x, want %#x", got, want)
	}
}

func TestIOC_Fields(t *testing.T) {
	// dir=READ|WRITE=3 @ shift 30, size=16 @ shift 16, type=0x75 @ shift 8, nr=0x21.
	got := ioc(iocRead|iocWrite, 'u', 0x21, 16)
	if dir := got >> iocDirShift & 0x3; dir != iocRead|iocWrite {
		t.Errorf("dir = %d, want %d", dir, iocRead|iocWrite)
	}
	if size := got >> iocSizeShift & 0x3fff; size != 16 {
		t.Errorf("size = %d, want 16", size)
	}
	if typ := got >> iocTypeShift & 0xff; typ != 'u' {
		t.Errorf("type = %#x, want %#x", typ, 'u')
	}
	if nr := got >> iocNRShift & 0xff; nr != 0x21 {
		t.Errorf("nr = %#x, want 0x21", nr)
	}
}
