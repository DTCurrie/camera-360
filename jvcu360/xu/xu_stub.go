//go:build !linux

package xu

// Non-Linux stub mirroring xu_linux.go's exported surface so the module builds
// and the switch component / probe tool compile on macOS and Windows. Every
// Extension-Unit operation returns ErrUnsupported (UVC XU control needs the
// Linux uvcvideo ioctl).

// Device is a no-op handle on non-Linux platforms.
type Device struct{}

// Open always fails on non-Linux platforms.
func Open(string) (*Device, error) { return nil, ErrUnsupported }

// Close is a no-op.
func (d *Device) Close() error { return nil }

func (d *Device) GetCur(unit, selector uint8, buf []byte) error { return ErrUnsupported }
func (d *Device) SetCur(unit, selector uint8, buf []byte) error { return ErrUnsupported }
func (d *Device) GetMin(unit, selector uint8, buf []byte) error { return ErrUnsupported }
func (d *Device) GetMax(unit, selector uint8, buf []byte) error { return ErrUnsupported }
func (d *Device) GetDef(unit, selector uint8, buf []byte) error { return ErrUnsupported }
func (d *Device) GetLen(unit, selector uint8) (int, error)      { return 0, ErrUnsupported }
func (d *Device) GetInfo(unit, selector uint8) (byte, error)    { return 0, ErrUnsupported }
