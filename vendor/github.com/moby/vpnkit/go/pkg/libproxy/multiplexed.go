package libproxy

import (
	"bufio"
	"bytes"
	"container/ring"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	maxBufferSize = 65536
)

type windowState struct {
	current uint64
	allowed uint64
}

func (w *windowState) String() string {
	return fmt.Sprintf("current %d, allowed %d", w.current, w.allowed)
}

func newWindowState() *windowState {
	return &windowState{}
}
func (w *windowState) size() int {
	return int(w.allowed - w.current)
}
func (w *windowState) isAlmostClosed() bool {
	return w.size() < maxBufferSize/2
}
func (w *windowState) advance() {
	w.allowed = w.current + uint64(maxBufferSize)
}

type channel struct {
	m             *sync.Mutex
	c             *sync.Cond
	multiplexer   *Multiplexer
	destination   Destination
	ID            uint32
	read          *windowState
	write         *windowState
	readPipe      *bufferedPipe
	closeReceived bool
	closeSent     bool
	// initially 2 (sender + receiver), protected by the multiplexer
	refCount                     int
	shutdownSent                 bool
	writeDeadline                time.Time
	testAllowDataAfterCloseWrite bool
}

func (c *channel) String() string {
	closeReceived := ""
	if c.closeReceived {
		closeReceived = "closeReceived "
	}
	closeSent := ""
	if c.closeSent {
		closeSent = "closeSent "
	}
	shutdownSent := ""
	if c.shutdownSent {
		shutdownSent = "shutdownSent "
	}
	return fmt.Sprintf("ID %d -> %s %s%s%s", c.ID, c.destination.String(), closeReceived, closeSent, shutdownSent)
}

// newChannel registers a channel through the multiplexer
func newChannel(multiplexer *Multiplexer, ID uint32, d Destination) *channel {
	var m sync.Mutex
	c := sync.NewCond(&m)
	readPipe := newBufferedPipe()
	return &channel{
		m:           &m,
		c:           c,
		multiplexer: multiplexer,
		destination: d,
		ID:          ID,
		read:        &windowState{},
		write:       &windowState{},
		readPipe:    readPipe,
		refCount:    2,
	}
}

func (c *channel) sendWindowUpdate() error {
	c.m.Lock()
	c.read.advance()
	seq := c.read.allowed
	c.m.Unlock()
	return c.multiplexer.send(NewWindow(c.ID, seq))
}

func (c *channel) recvWindowUpdate(seq uint64) {
	c.m.Lock()
	c.write.allowed = seq
	c.c.Signal()
	c.m.Unlock()
}

func (c *channel) Read(p []byte) (int, error) {
	n, err := c.readPipe.Read(p)
	c.m.Lock()
	c.read.current = c.read.current + uint64(n)
	needUpdate := c.read.isAlmostClosed()
	c.m.Unlock()
	if needUpdate {
		c.sendWindowUpdate()
	}
	return n, err
}

// for unit testing only
func (c *channel) setTestAllowDataAfterCloseWrite() {
	c.testAllowDataAfterCloseWrite = true
}

func (c *channel) Write(p []byte) (int, error) {
	c.m.Lock()
	defer c.m.Unlock()
	written := 0
	for {
		if len(p) == 0 {
			return written, nil
		}
		if c.closeReceived || c.closeSent || (c.shutdownSent && !c.testAllowDataAfterCloseWrite) {
			return written, io.EOF
		}
		if c.write.size() > 0 {
			toWrite := c.write.size()
			if toWrite > len(p) {
				toWrite = len(p)
			}
			// need to write the header and the payload together
			c.multiplexer.writeMutex.Lock()
			f := NewData(c.ID, uint32(toWrite))
			c.multiplexer.appendEvent(&event{eventType: eventSend, frame: f})
			err1 := f.Write(c.multiplexer.connW)
			_, err2 := c.multiplexer.connW.Write(p[0:toWrite])
			err3 := c.multiplexer.connW.Flush()
			c.multiplexer.writeMutex.Unlock()

			if err1 != nil {
				return written, err1
			}
			if err2 != nil {
				return written, err2
			}
			if err3 != nil {
				return written, err3
			}
			c.write.current = c.write.current + uint64(toWrite)
			p = p[toWrite:]
			written = written + toWrite
			continue
		}

		// Wait for the write window to be increased (or a timeout)
		done := make(chan struct{})
		timeout := make(chan time.Time)
		if !c.writeDeadline.IsZero() {
			go func() {
				time.Sleep(time.Until(c.writeDeadline))
				close(timeout)
			}()
		}
		go func() {
			c.c.Wait()
			close(done)
		}()
		select {
		case <-timeout:
			// clean up the goroutine
			c.c.Broadcast()
			<-done
			return written, &errTimeout{}
		case <-done:
			// The timeout will still fire in the background
			continue
		}
	}
}

func (c *channel) Close() error {
	// Avoid a Write() racing with us and sending after we Close()
	// Avoid sending Close twice
	c.m.Lock()
	alreadyClosed := c.closeSent
	c.closeSent = true
	c.m.Unlock()

	if alreadyClosed {
		return nil
	}
	if err := c.multiplexer.send(NewClose(c.ID)); err != nil {
		return err
	}
	c.m.Lock()
	defer c.m.Unlock()
	c.c.Broadcast()

	c.multiplexer.decrChannelRef(c.ID)

	return nil
}

func (c *channel) CloseRead() error {
	return c.readPipe.CloseWrite()
}

func (c *channel) CloseWrite() error {
	// Avoid a Write() racing with us and sending after we Close()
	// Avoid sending Shutdown twice
	c.m.Lock()
	alreadyShutdown := c.shutdownSent || c.closeSent
	c.shutdownSent = true
	c.m.Unlock()

	if alreadyShutdown {
		return nil
	}
	if err := c.multiplexer.send(NewShutdown(c.ID)); err != nil {
		return err
	}
	c.m.Lock()
	defer c.m.Unlock()
	c.c.Broadcast()
	return nil
}

func (c *channel) recvClose() {
	c.m.Lock()
	defer c.m.Unlock()
	c.closeReceived = true
	c.c.Broadcast()
}

func (c *channel) SetReadDeadline(timeout time.Time) error {
	return c.readPipe.SetReadDeadline(timeout)
}

func (c *channel) SetWriteDeadline(timeout time.Time) error {
	c.m.Lock()
	defer c.m.Unlock()
	c.writeDeadline = timeout
	c.c.Broadcast()
	return nil
}

func (c *channel) SetDeadline(timeout time.Time) error {
	if err := c.SetReadDeadline(timeout); err != nil {
		return err
	}
	return c.SetWriteDeadline(timeout)
}

func (c *channel) RemoteAddr() net.Addr {
	return &channelAddr{
		d: c.destination,
	}
}

func (c *channel) LocalAddr() net.Addr {
	return c.RemoteAddr() // There is no local address
}

type channelAddr struct {
	d Destination
}

func (a *channelAddr) Network() string {
	return "channel"
}

func (a *channelAddr) String() string {
	return a.d.String()
}

const (
	eventSend  = 0
	eventRecv  = 1
	eventOpen  = 2
	eventClose = 3
)

type event struct {
	eventType   int
	frame       *Frame      // for eventSend and eventRecv
	id          uint32      // for eventOpen and eventClose
	destination Destination // for eventOpen and eventClose
}

func (e *event) String() string {
	switch e.eventType {
	case eventSend:
		return fmt.Sprintf("send  %s", e.frame.String())
	case eventRecv:
		return fmt.Sprintf("recv  %s", e.frame.String())
	case eventOpen:
		return fmt.Sprintf("open  %d -> %s", e.id, e.destination)
	case eventClose:
		return fmt.Sprintf("close %d -> %s", e.id, e.destination)
	default:
		return "unknown trace event"
	}
}

// Multiplexer muxes and demuxes sub-connections over a single connection
type Multiplexer struct {
	label         string
	conn          io.Closer
	connR         io.Reader // with buffering
	connW         *bufio.Writer
	writeMutex    *sync.Mutex // hold when writing on the channel
	channels      map[uint32]*channel
	nextChannelID uint32
	metadataMutex *sync.Mutex // hold when reading/modifying this structure
	pendingAccept []*channel  // incoming connections
	acceptCond    *sync.Cond
	isRunning     bool
	events        *ring.Ring // log of packetEvents
	eventsM       *sync.Mutex
}

// NewMultiplexer constructs a multiplexer from a channel
func NewMultiplexer(label string, conn io.ReadWriteCloser) *Multiplexer {
	var writeMutex, metadataMutex, eventsM sync.Mutex
	acceptCond := sync.NewCond(&metadataMutex)
	channels := make(map[uint32]*channel)
	connR := bufio.NewReader(conn)
	connW := bufio.NewWriter(conn)
	events := ring.New(500)
	return &Multiplexer{
		label:         label,
		conn:          conn,
		connR:         connR,
		connW:         connW,
		writeMutex:    &writeMutex,
		channels:      channels,
		metadataMutex: &metadataMutex,
		acceptCond:    acceptCond,
		nextChannelID: ^uint32(0),
		events:        events,
		eventsM:       &eventsM,
	}
}

// Close the underlying transport.
func (m *Multiplexer) Close() error {
	return m.conn.Close()
}

func (m *Multiplexer) appendEvent(e *event) {
	m.eventsM.Lock()
	defer m.eventsM.Unlock()
	m.events.Value = e
	m.events = m.events.Next()
}

func (m *Multiplexer) send(f *Frame) error {
	m.writeMutex.Lock()
	defer m.writeMutex.Unlock()
	if err := f.Write(m.connW); err != nil {
		return err
	}
	m.appendEvent(&event{eventType: eventSend, frame: f})
	return m.connW.Flush()
}

func (m *Multiplexer) findFreeChannelID() uint32 {
	// the metadataMutex is already held
	id := m.nextChannelID
	for {
		if _, ok := m.channels[id]; !ok {
			m.nextChannelID = id + 1
			return id
		}
		id++
	}
}

func (m *Multiplexer) decrChannelRef(ID uint32) {
	m.metadataMutex.Lock()
	defer m.metadataMutex.Unlock()
	if channel, ok := m.channels[ID]; ok {
		if channel.refCount == 1 {
			m.appendEvent(&event{eventType: eventClose, id: ID, destination: channel.destination})
			delete(m.channels, ID)
			return
		}
		channel.refCount = channel.refCount - 1
	}
}

// Dial opens a connection to the given destination
func (m *Multiplexer) Dial(d Destination) (Conn, error) {
	m.metadataMutex.Lock()
	if !m.isRunning {
		m.metadataMutex.Unlock()
		return nil, errors.New("connection refused")
	}
	id := m.findFreeChannelID()
	channel := newChannel(m, id, d)
	m.channels[id] = channel
	m.metadataMutex.Unlock()

	if err := m.send(NewOpen(id, d)); err != nil {
		return nil, err
	}
	if err := channel.sendWindowUpdate(); err != nil {
		return nil, err
	}
	return channel, nil
}

// Accept returns the next client connection
func (m *Multiplexer) Accept() (Conn, *Destination, error) {
	m.metadataMutex.Lock()
	defer m.metadataMutex.Unlock()
	for {
		if len(m.pendingAccept) > 0 {
			first := m.pendingAccept[0]
			m.pendingAccept = m.pendingAccept[1:]
			if err := first.sendWindowUpdate(); err != nil {
				return nil, nil, err
			}
			return first, &first.destination, nil
		}
		m.acceptCond.Wait()
	}
}

// Run starts handling the requests from the other side
func (m *Multiplexer) Run() {
	m.metadataMutex.Lock()
	m.isRunning = true
	m.metadataMutex.Unlock()
	go func() {
		if err := m.run(); err != nil {
			log.Printf("Multiplexer main loop failed with %v", err)
			log.Printf("Event trace:")
			m.events.Do(func(p interface{}) {
				if e, ok := p.(*event); ok {
					log.Print(e.String())
				}
			})
			log.Printf("Active channels:")
			for _, c := range m.channels {
				log.Printf("%s", c.String())
			}
		}
		m.metadataMutex.Lock()
		m.isRunning = false
		m.metadataMutex.Unlock()

		// close all open channels
		for _, channel := range m.channels {
			// this will unblock waiting Read calls
			channel.readPipe.closeWriteNoErr()
			// this will unblock waiting Write calls
			channel.recvClose()
			m.decrChannelRef(channel.ID)
		}
	}()
}

// IsRunning returns whether the multiplexer is running or not
func (m *Multiplexer) IsRunning() bool {
	m.metadataMutex.Lock()
	defer m.metadataMutex.Unlock()
	return m.isRunning
}

func (m *Multiplexer) run() error {
	for {
		f, err := unmarshalFrame(m.connR)
		if err != nil {
			return fmt.Errorf("Failed to unmarshal command frame: %v", err)
		}
		m.appendEvent(&event{eventType: eventRecv, frame: f})
		switch payload := f.Payload().(type) {
		case *OpenFrame:
			o, err := f.Open()
			if err != nil {
				return fmt.Errorf("Failed to unmarshal open command: %v", err)
			}
			switch o.Connection {
			case Dedicated:
				return fmt.Errorf("Dedicated connections are not implemented yet")
			case Multiplexed:
				m.metadataMutex.Lock()
				channel := newChannel(m, f.ID, o.Destination)
				m.channels[f.ID] = channel
				m.pendingAccept = append(m.pendingAccept, channel)
				m.acceptCond.Signal()
				m.metadataMutex.Unlock()
				m.appendEvent(&event{eventType: eventOpen, id: f.ID, destination: o.Destination})
			}
		case *WindowFrame:
			m.metadataMutex.Lock()
			channel, ok := m.channels[f.ID]
			m.metadataMutex.Unlock()
			if !ok {
				return fmt.Errorf("Unknown channel id %s", f.String())
			}
			channel.recvWindowUpdate(payload.seq)
		case *DataFrame:
			m.metadataMutex.Lock()
			channel, ok := m.channels[f.ID]
			m.metadataMutex.Unlock()
			if !ok {
				return fmt.Errorf("Unknown channel id: %s", f.String())
			}
			// We don't use a direct io.Copy or io.CopyN to the readPipe because if they get
			// EOF on Write, they will drop the data in the buffer and we don't know how big
			// it was so we can't avoid desychronising the stream.
			// We trust the clients not to write more than a Window size.
			var buf bytes.Buffer
			if _, err := io.CopyN(&buf, m.connR, int64(payload.payloadlen)); err != nil {
				return fmt.Errorf("Failed to read payload of %d bytes: %s", payload.payloadlen, f.String())
			}
			if n, err := io.Copy(channel.readPipe, &buf); err != nil {
				// err must be io.EOF
				log.Printf("Discarded %d bytes from %s", int64(payload.payloadlen)-n, f.String())
				// A confused client could send a DataFrame after a ShutdownFrame or CloseFrame.
				// The stream is not desychronised so we can keep going.
			}
		case *ShutdownFrame:
			m.metadataMutex.Lock()
			channel, ok := m.channels[f.ID]
			m.metadataMutex.Unlock()
			if !ok {
				return fmt.Errorf("Unknown channel id: %s", f.String())
			}
			channel.readPipe.closeWriteNoErr()
		case *CloseFrame:
			m.metadataMutex.Lock()
			channel, ok := m.channels[f.ID]
			m.metadataMutex.Unlock()
			if !ok {
				return fmt.Errorf("Unknown channel id: %s", f.String())
			}
			// this will unblock waiting Read calls
			channel.readPipe.closeWriteNoErr()
			// this will unblock waiting Write calls
			channel.recvClose()
			m.decrChannelRef(channel.ID)
		default:
			return fmt.Errorf("Unknown command type: %v", f)
		}
	}
}

// Forward runs the TCP/UDP forwarder over a sub-connection
func Forward(conn Conn, destination Destination, quit chan struct{}) {
	defer conn.Close()

	switch destination.Proto {
	case TCP:
		backendAddr := net.TCPAddr{IP: destination.IP, Port: int(destination.Port), Zone: ""}
		if err := HandleTCPConnection(conn, &backendAddr, quit); err != nil {
			log.Printf("Error setting up TCP proxy subconnection: %v", err)
			return
		}
	case Unix:
		backendAddr, err := net.ResolveUnixAddr("unix", destination.Path)
		if err != nil {
			log.Printf("Error resolving Unix address %s", destination.Path)
			return
		}
		if err := HandleUnixConnection(conn, backendAddr, quit); err != nil {
			log.Printf("Error setting up Unix proxy subconnection: %v", err)
			return
		}
	case UDP:
		backendAddr := &net.UDPAddr{IP: destination.IP, Port: int(destination.Port), Zone: ""}

		proxy, err := NewUDPProxy(backendAddr, NewUDPConn(conn), backendAddr)
		if err != nil {
			log.Printf("Failed to setup UDP proxy for %s: %#v", backendAddr, err)
			return
		}
		proxy.Run()
		return
	default:
		log.Printf("Unknown protocol: %d", destination.Proto)
		return
	}
}
