package xu

// Linux ioctl request-number encoding (asm-generic/ioctl.h). The bit layout is
// identical on the architectures this module targets (linux/amd64, linux/arm64);
// the handful of arches that differ (alpha, mips, powerpc, sparc) are out of
// scope. Kept in an untagged file so the encoding is unit-testable on any OS.
const (
	iocNRBits   = 8
	iocTypeBits = 8
	iocSizeBits = 14

	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits     // 8
	iocSizeShift = iocTypeShift + iocTypeBits // 16
	iocDirShift  = iocSizeShift + iocSizeBits // 30

	iocNone  = 0
	iocWrite = 1
	iocRead  = 2
)

// ioc builds an ioctl request number from a direction, type ("magic"), command
// number, and payload size.
func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<iocDirShift | typ<<iocTypeShift | nr<<iocNRShift | size<<iocSizeShift
}

// iowr is _IOWR(typ, nr, size): a bidirectional ioctl (kernel both reads the
// request payload and writes a response into it).
func iowr(typ, nr, size uintptr) uintptr {
	return ioc(iocRead|iocWrite, typ, nr, size)
}
