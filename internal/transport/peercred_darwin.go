//go:build darwin

package transport

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// VerifyPeerCredentials checks that the peer on a Unix domain socket
// is running under the same user ID (UID) as the current process.
func VerifyPeerCredentials(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil
	}
	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("getting syscall conn: %w", err)
	}
	var credErr error
	err = rawConn.Control(func(fd uintptr) {
		xucred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			credErr = fmt.Errorf("getsockopt LOCAL_PEERCRED: %w", err)
			return
		}
		if xucred.Uid != uint32(os.Getuid()) {
			credErr = fmt.Errorf("peer UID %d does not match process UID %d", xucred.Uid, os.Getuid())
			return
		}
	})
	if err != nil {
		return err
	}
	return credErr
}
