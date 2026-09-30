//go:build !linux && !darwin

package transport

import "net"

// VerifyPeerCredentials is a no-op on platforms without Unix peercred support.
func VerifyPeerCredentials(conn net.Conn) error {
	return nil
}
