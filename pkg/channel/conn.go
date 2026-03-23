package channel

import (
	"bytes"
	"io"
	"net"
	"sync"
	"time"
)

// Conn wraps a mux channel as a net.Conn for use with go-redis.
// Writes are framed with the channel ID and sent to the mux writer.
// Reads pull from a buffer fed by the mux's read loop.
type Conn struct {
	channelID uint32
	writer    io.Writer // mux's framed writer (shared, mu-protected)
	writeMu   *sync.Mutex

	readBuf bytes.Buffer
	readMu  sync.Mutex
	readCh  chan struct{} // signaled when new data arrives
	closed  chan struct{}
}

func newConn(channelID uint32, writer io.Writer, writeMu *sync.Mutex) *Conn {
	return &Conn{
		channelID: channelID,
		writer:    writer,
		writeMu:   writeMu,
		readCh:    make(chan struct{}, 1),
		closed:    make(chan struct{}),
	}
}

// deliver is called by the mux read loop to push response data into this channel.
func (c *Conn) deliver(data []byte) {
	c.readMu.Lock()
	c.readBuf.Write(data)
	c.readMu.Unlock()
	select {
	case c.readCh <- struct{}{}:
	default:
	}
}

func (c *Conn) Read(p []byte) (int, error) {
	for {
		c.readMu.Lock()
		n, _ := c.readBuf.Read(p)
		c.readMu.Unlock()
		if n > 0 {
			return n, nil
		}
		select {
		case <-c.readCh:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
}

func (c *Conn) Write(p []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.writeMu.Lock()
	err := WriteFrame(c.writer, c.channelID, p)
	c.writeMu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *Conn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *Conn) LocalAddr() net.Addr  { return channelAddr(c.channelID) }
func (c *Conn) RemoteAddr() net.Addr { return channelAddr(c.channelID) }

func (c *Conn) SetDeadline(_ time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(_ time.Time) error   { return nil }
func (c *Conn) SetWriteDeadline(_ time.Time) error  { return nil }

type channelAddr uint32

func (a channelAddr) Network() string { return "channel" }
func (a channelAddr) String() string  { return "channel" }
