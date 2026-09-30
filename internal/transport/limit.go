package transport

import (
	"errors"
	"net"
	"sync"
)

// LimitListener returns a Listener that accepts at most max simultaneous connections.
// Additional connections wait until an active connection closes.
// If max <= 0, no limit is applied.
func LimitListener(l net.Listener, max int) net.Listener {
	if max <= 0 {
		return l
	}
	return &limitListener{
		Listener: l,
		sem:      make(chan struct{}, max),
		done:     make(chan struct{}),
	}
}

type limitListener struct {
	net.Listener
	sem       chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func (l *limitListener) acquire() bool {
	select {
	case <-l.done:
		return false
	case l.sem <- struct{}{}:
		return true
	}
}

func (l *limitListener) release() {
	select {
	case <-l.sem:
	default:
	}
}

func (l *limitListener) Accept() (net.Conn, error) {
	if !l.acquire() {
		return nil, errors.New("listener closed")
	}

	c, err := l.Listener.Accept()
	if err != nil {
		l.release()
		return nil, err
	}

	return &limitListenerConn{Conn: c, release: l.release}, nil
}

func (l *limitListener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.Listener.Close()
	})
	return err
}

type limitListenerConn struct {
	net.Conn
	release   func()
	closeOnce sync.Once
}

func (c *limitListenerConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.release()
		err = c.Conn.Close()
	})
	return err
}
