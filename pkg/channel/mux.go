package channel

import (
	"fmt"
	"io"
	"net"
	"sync"
)

// Mux multiplexes multiple logical channels over a single bidirectional stream.
// Each channel appears as an independent net.Conn to the consumer.
//
// For in-process Go modules, both sides are connected via io.Pipe().
// For WASM modules, the stream maps to stdin/stdout.
type Mux struct {
	reader  io.Reader
	writer  io.Writer
	writeMu sync.Mutex

	channels map[uint32]*Conn
	mu       sync.RWMutex

	closed chan struct{}
}

// NewMux creates a multiplexer over the given bidirectional stream.
// Call ReadLoop() in a goroutine to start dispatching incoming frames to channels.
func NewMux(r io.Reader, w io.Writer) *Mux {
	return &Mux{
		reader:   r,
		writer:   w,
		channels: make(map[uint32]*Conn),
		closed:   make(chan struct{}),
	}
}

// Channel returns a net.Conn for the given channel ID.
// Creates the channel on first call. Subsequent calls return the same Conn.
func (m *Mux) Channel(id uint32) net.Conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.channels[id]; ok {
		return c
	}
	c := newConn(id, m.writer, &m.writeMu)
	m.channels[id] = c
	return c
}

// ReadLoop reads framed messages from the stream and dispatches payloads to
// the appropriate channel's read buffer. Blocks until the stream is closed
// or an error occurs.
func (m *Mux) ReadLoop() error {
	for {
		select {
		case <-m.closed:
			return nil
		default:
		}
		channelID, payload, err := ReadFrame(m.reader)
		if err != nil {
			select {
			case <-m.closed:
				return nil
			default:
				return fmt.Errorf("mux read: %w", err)
			}
		}
		m.mu.RLock()
		c, ok := m.channels[channelID]
		m.mu.RUnlock()
		if ok {
			c.deliver(payload)
		}
	}
}

// Close shuts down the mux and all channels.
func (m *Mux) Close() error {
	select {
	case <-m.closed:
		return nil
	default:
		close(m.closed)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.channels {
		c.Close()
	}
	return nil
}

// HostBridge reads framed requests from a module's output, bridges each
// channel to a backend net.Conn, and writes framed responses back.
//
// The dial function is called once per channel ID to establish the backend
// connection (e.g., dial the redcon server). The bridge then copies data
// bidirectionally between the framed channel and the backend connection.
//
// Blocks until the reader is closed or an error occurs.
func HostBridge(r io.Reader, w io.Writer, dial func(channelID uint32) (net.Conn, error)) error {
	var (
		backends = make(map[uint32]net.Conn)
		writeMu  sync.Mutex
		mu       sync.Mutex
	)

	getBackend := func(channelID uint32) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		if conn, ok := backends[channelID]; ok {
			return conn, nil
		}
		conn, err := dial(channelID)
		if err != nil {
			return nil, fmt.Errorf("dial backend for channel %d: %w", channelID, err)
		}
		backends[channelID] = conn

		// Read responses from backend and frame them back to the module.
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					writeMu.Lock()
					WriteFrame(w, channelID, buf[:n])
					writeMu.Unlock()
				}
				if err != nil {
					return
				}
			}
		}()

		return conn, nil
	}

	for {
		channelID, payload, err := ReadFrame(r)
		if err != nil {
			return fmt.Errorf("host bridge read: %w", err)
		}
		backend, err := getBackend(channelID)
		if err != nil {
			return err
		}
		if _, err := backend.Write(payload); err != nil {
			return fmt.Errorf("host bridge write to backend: %w", err)
		}
	}
}

// Dial creates an in-process channel pair connected via io.Pipe().
// Returns the module-side net.Conn and starts a HostBridge goroutine that
// routes all traffic to the backend obtained via the dial function.
//
// This is the simplest way to wire up a single channel for in-process Go modules.
func Dial(dial func() (net.Conn, error)) (net.Conn, error) {
	moduleRead, hostWrite := io.Pipe()
	hostRead, moduleWrite := io.Pipe()

	mux := NewMux(moduleRead, moduleWrite)

	go func() {
		HostBridge(hostRead, hostWrite, func(_ uint32) (net.Conn, error) {
			return dial()
		})
	}()

	go mux.ReadLoop()

	return mux.Channel(0), nil
}
