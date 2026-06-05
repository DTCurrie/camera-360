//go:build linux

package xu

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// UVC Video Class-Specific Request Codes (USB Video Class 1.5 §A.8). SET_CUR
// writes a control; the GET_* requests read its current/range/length/info.
const (
	reqSetCur  = 0x01
	reqGetCur  = 0x81
	reqGetMin  = 0x82
	reqGetMax  = 0x83
	reqGetRes  = 0x84
	reqGetLen  = 0x85
	reqGetInfo = 0x86
	reqGetDef  = 0x87
)

// ctrlQuery mirrors C `struct uvc_xu_control_query` from <linux/uvcvideo.h>:
//
//	struct uvc_xu_control_query { __u8 unit; __u8 selector; __u8 query;
//	                              __u16 size; __u8 __user *data; };
//
// It is NOT __packed in the kernel, so natural alignment applies. On the LP64
// targets (linux/amd64, linux/arm64) that lays out as unit@0 selector@1 query@2
// _pad@3 size@4 _pad@6 data@8 — total 16 bytes. The explicit pad fields pin that
// layout so it matches the C struct regardless of Go's alignment choices; a test
// asserts the size is 16.
type ctrlQuery struct {
	unit     uint8
	selector uint8
	query    uint8
	_        uint8
	size     uint16
	_        uint16
	data     uintptr // __u8 __user *data
}

// uvcIOCCtrlQuery is _IOWR('u', 0x21, struct uvc_xu_control_query). The size is
// derived from the Go struct so the encoded size can never drift from what we
// actually pass to the kernel. (0x20 is UVCIOC_CTRL_MAP; 0x21 is _QUERY.)
var uvcIOCCtrlQuery = iowr('u', 0x21, unsafe.Sizeof(ctrlQuery{}))

// Device is an opened V4L2 node used to issue UVC Extension-Unit control
// queries. Opening it does not start streaming, and the ioctls go through the
// uvcvideo driver, so a Device can coexist with an ffmpeg capture on the same
// node.
type Device struct {
	fd   int
	path string
}

// Open opens the V4L2 node at path (e.g. "/dev/video0") for XU control.
func Open(path string) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Device{fd: fd, path: path}, nil
}

// Close releases the underlying file descriptor.
func (d *Device) Close() error {
	if d == nil {
		return nil
	}
	return unix.Close(d.fd)
}

// query runs one UVCIOC_CTRL_QUERY. buf is the input for SET_CUR and the output
// for the GET_* requests; its length is sent as the control size.
func (d *Device) query(unit, selector, req uint8, buf []byte) error {
	if len(buf) == 0 {
		return errors.New("jvcu360: empty control buffer")
	}
	q := ctrlQuery{
		unit:     unit,
		selector: selector,
		query:    req,
		size:     uint16(len(buf)),
		data:     uintptr(unsafe.Pointer(&buf[0])),
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), uvcIOCCtrlQuery, uintptr(unsafe.Pointer(&q)))
	// The kernel only touches buf/q during the syscall, but q.data is a uintptr
	// the GC can't follow, so keep buf alive until the syscall returns.
	runtime.KeepAlive(buf)
	runtime.KeepAlive(&q)
	if errno != 0 {
		return fmt.Errorf("uvc xu query unit=%d sel=%d req=%#x on %s: %w", unit, selector, req, d.path, errno)
	}
	return nil
}

// GetCur/SetCur/GetMin/GetMax/GetDef issue the corresponding request, reading
// into or writing from buf (sized by the caller, usually from GetLen).
func (d *Device) GetCur(unit, selector uint8, buf []byte) error {
	return d.query(unit, selector, reqGetCur, buf)
}

func (d *Device) SetCur(unit, selector uint8, buf []byte) error {
	return d.query(unit, selector, reqSetCur, buf)
}

func (d *Device) GetMin(unit, selector uint8, buf []byte) error {
	return d.query(unit, selector, reqGetMin, buf)
}

func (d *Device) GetMax(unit, selector uint8, buf []byte) error {
	return d.query(unit, selector, reqGetMax, buf)
}

func (d *Device) GetDef(unit, selector uint8, buf []byte) error {
	return d.query(unit, selector, reqGetDef, buf)
}

// GetLen returns the control's data length (UVC GET_LEN yields a 2-byte
// little-endian word), used to size the buffers for the other requests.
func (d *Device) GetLen(unit, selector uint8) (int, error) {
	var b [2]byte
	if err := d.query(unit, selector, reqGetLen, b[:]); err != nil {
		return 0, err
	}
	return int(b[0]) | int(b[1])<<8, nil
}

// GetInfo returns the 1-byte capability bitmap (UVC GET_INFO): bit0 GET, bit1
// SET, bit2 disabled, bit3 autoupdate, bit4 async.
func (d *Device) GetInfo(unit, selector uint8) (byte, error) {
	var b [1]byte
	if err := d.query(unit, selector, reqGetInfo, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}
