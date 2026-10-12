package tunnel

import (
	"bufio"
	"io"
	"net"
	"sync"
	"time"
)

// bufferedConn is a hijacked connection whose first bytes may already sit in
// the server's bufio.Reader.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// watchedConn closes done the first time the connection fails or is closed.
type watchedConn struct {
	net.Conn
	once sync.Once
	done chan struct{}
}

func (c *watchedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.fail()
	}
	return n, err
}

func (c *watchedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil {
		c.fail()
	}
	return n, err
}

func (c *watchedConn) Close() error {
	c.fail()
	return c.Conn.Close()
}

func (c *watchedConn) fail() { c.once.Do(func() { close(c.done) }) }

// rwcConn adapts the io.ReadWriteCloser body of a 101 response to net.Conn.
type rwcConn struct{ io.ReadWriteCloser }

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "tsrelay-tunnel" }
func (tunnelAddr) String() string  { return "tsrelay-tunnel" }

func (rwcConn) LocalAddr() net.Addr              { return tunnelAddr{} }
func (rwcConn) RemoteAddr() net.Addr             { return tunnelAddr{} }
func (rwcConn) SetDeadline(time.Time) error      { return nil }
func (rwcConn) SetReadDeadline(time.Time) error  { return nil }
func (rwcConn) SetWriteDeadline(time.Time) error { return nil }
