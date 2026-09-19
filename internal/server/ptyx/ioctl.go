package ptyx

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
)

// setsize issues TIOCSWINSZ through the master's SyscallConn rather than
// calling pty.Setsize directly. pty.Setsize calls f.Fd(), which hands the
// raw descriptor to the ioctl syscall without holding any reference to
// the underlying poll.FD. Kill's Master.Close can run concurrently with a
// Resize -- see Pane.Close's doc comment in ../pane.go for the window
// that reopens once resizeMu is no longer held across both -- and on a
// bare Fd() that means the ioctl can land on a stale, reused descriptor
// once Close has torn the old one down: not memory-unsafe, but a
// misdirected TIOCSWINSZ at best (ENOTTY or a resize of an unrelated pty
// at worst). SyscallConn's Control increfs the poll.FD for the duration
// of the call and returns an error once the file is closed instead,
// eliminating both the race and the reuse hazard.
//
// creack/pty ships exactly this fix as ioctlNonblock (ioctl.go:13),
// marked "NOTE: Unused. Keeping for reference." It isn't exported, so
// this reimplements it for the one ioctl wideboi issues.
func setsize(f *os.File, ws *pty.Winsize) error {
	sc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	ctrlErr := sc.Control(func(fd uintptr) {
		//nolint:gosec // Expected unsafe pointer for a Winsize ioctl.
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws))); errno != 0 {
			ioctlErr = errno
		}
	})
	if ctrlErr != nil {
		return ctrlErr
	}
	return ioctlErr
}
