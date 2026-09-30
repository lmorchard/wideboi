//go:build linux

package transport

import (
	"fmt"
	"net"
	"os"
	"syscall"

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
		ucred, err := unix.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			credErr = fmt.Errorf("getsockopt SO_PEERCRED: %w", err)
			return
		}
		if ucred.Uid != uint32(os.Getuid()) {
			credErr = fmt.Errorf("peer UID %d does not match process UID %d", ucred.Uid, os.Getuid())
			return
		}
	})
	if err != nil {
		return err
	}
	return credErr
}
