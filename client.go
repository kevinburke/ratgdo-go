package ratgdo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/mycontroller-org/esphome_api/pkg/connection"
	"google.golang.org/protobuf/proto"
)

// Errors returned by Client methods.
var (
	// ErrClosed is returned by commands issued after Close.
	ErrClosed = errors.New("ratgdo: client closed")
	// ErrNoEntity is returned when the firmware doesn't expose the entity
	// a command requires — i.e. this library is talking to something that
	// isn't a ratgdo after all.
	ErrNoEntity = errors.New("ratgdo: device does not expose the required entity")
)

// Default values for a Client; override with options.
const (
	defaultTimeout        = 10 * time.Second
	defaultClientID       = "ratgdo-go"
	defaultSubscriberCap  = 64
	initialReconnectDelay = 500 * time.Millisecond
	maxReconnectDelay     = 30 * time.Second
)

// Config holds the optional knobs for a Client. Pass nil to Dial for all
// defaults, or set only the fields you care about (zero values mean "use
// the default").
type Config struct {
	// ClientID is sent to the device as ClientInfo in the Hello handshake
	// and appears in the device's logs. Defaults to "ratgdo-go".
	ClientID string
	// Timeout bounds each network operation: the initial dial, the Noise
	// handshake, and individual wire writes. Defaults to 10s.
	Timeout time.Duration
	// Logger handles reconnect/error logging. Defaults to slog.Default().
	Logger *slog.Logger
}

// Client is a long-lived connection to a ratgdo device. Construct it with
// Dial; release it with Close.
type Client struct {
	addr     string
	encKey   string
	clientID string
	timeout  time.Duration
	logger   *slog.Logger

	closeCh chan struct{}
	doneCh  chan struct{}

	mu              sync.Mutex
	apiConn         connection.ApiConnection
	tcpConn         net.Conn
	readerForManage *bufio.Reader
	entities        map[string]uint32
	state           State
	connected       bool
	// stateCh is closed every time c.connected flips in either direction,
	// waking anyone blocked in waitConnected. A fresh channel replaces it.
	stateCh     chan struct{}
	subscribers []chan Event
	waiters     map[uint64]chan proto.Message
	closed      bool
}

// Dial opens a connection to the ratgdo at addr (e.g. "ratgdo.local:6053")
// and performs the Noise handshake with the given base64 encryption key. If
// encryptionKey is empty, a plaintext session is used — only do that on a
// completely trusted network.
//
// Pass nil for cfg to accept all defaults. If the initial session setup
// fails, Dial returns the error and no Client.
//
// After Dial returns, the Client maintains the session in the background:
// on disconnect it reconnects with exponential backoff until Close.
func Dial(ctx context.Context, addr, encryptionKey string, cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	c := &Client{
		addr:     addr,
		encKey:   encryptionKey,
		clientID: valueOr(cfg.ClientID, defaultClientID),
		timeout:  durationOr(cfg.Timeout, defaultTimeout),
		logger:   loggerOr(cfg.Logger, slog.Default()),
		closeCh:  make(chan struct{}),
		doneCh:   make(chan struct{}),
		entities: map[string]uint32{},
		stateCh:  make(chan struct{}),
	}

	if err := c.connect(ctx); err != nil {
		return nil, err
	}

	go c.manage()
	return c, nil
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func durationOr(v, def time.Duration) time.Duration {
	if v == 0 {
		return def
	}
	return v
}

func loggerOr(v, def *slog.Logger) *slog.Logger {
	if v == nil {
		return def
	}
	return v
}

// Close disconnects from the device, stops the background reconnect loop,
// and closes all Subscribe channels. It is safe to call Close more than
// once; subsequent calls return nil.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	tcp := c.tcpConn
	// Unblock any waitConnected callers.
	select {
	case <-c.stateCh:
	default:
		close(c.stateCh)
	}
	c.mu.Unlock()

	close(c.closeCh)
	if tcp != nil {
		_ = tcp.Close()
	}
	<-c.doneCh

	c.mu.Lock()
	for _, ch := range c.subscribers {
		close(ch)
	}
	c.subscribers = nil
	c.mu.Unlock()
	return nil
}

// Connected reports whether the client currently has a live, authenticated
// session with the device.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// State returns a snapshot of the most recently observed device state.
// Safe to call at any time, including while disconnected (returns stale
// data — check State.LastSeenAt).
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Subscribe returns a channel that receives every state change plus
// connect/disconnect notifications. The channel is buffered; if a consumer
// falls more than the buffer behind, older events are dropped (logged). The
// channel closes when Close is called.
func (c *Client) Subscribe() <-chan Event {
	ch := make(chan Event, defaultSubscriberCap)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		close(ch)
		return ch
	}
	c.subscribers = append(c.subscribers, ch)
	return ch
}

// WaitFor blocks until pred(c.State()) returns true, ctx expires, or the
// client is closed. pred is evaluated against the current state on every
// received event.
func (c *Client) WaitFor(ctx context.Context, pred func(State) bool) error {
	if pred(c.State()) {
		return nil
	}
	ch := c.Subscribe()
	defer c.unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return ErrClosed
			}
			if pred(ev.Curr) {
				return nil
			}
		}
	}
}

func (c *Client) unsubscribe(ch <-chan Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.subscribers {
		if s == ch {
			c.subscribers = append(c.subscribers[:i], c.subscribers[i+1:]...)
			// Drain and close so the receiver unblocks.
			close(s)
			return
		}
	}
}

// --- Commands -------------------------------------------------------------

// OpenDoor sends an open command to the opener. The call returns as soon as
// the command is on the wire; observe State or Subscribe to confirm the
// door actually moved.
func (c *Client) OpenDoor(ctx context.Context) error {
	return c.coverCommand(ctx, api.LegacyCoverCommand_LEGACY_COVER_COMMAND_OPEN)
}

// CloseDoor sends a close command.
func (c *Client) CloseDoor(ctx context.Context) error {
	return c.coverCommand(ctx, api.LegacyCoverCommand_LEGACY_COVER_COMMAND_CLOSE)
}

// StopDoor halts the door if it is currently moving.
func (c *Client) StopDoor(ctx context.Context) error {
	return c.coverCommand(ctx, api.LegacyCoverCommand_LEGACY_COVER_COMMAND_STOP)
}

// SetDoorPosition drives the door to the given fractional position
// (0 = fully closed, 1 = fully open). Ratgdo supports this natively.
func (c *Client) SetDoorPosition(ctx context.Context, position float32) error {
	if position < 0 || position > 1 {
		return fmt.Errorf("ratgdo: position %v out of range [0,1]", position)
	}
	key, ok := c.entityKey("cover:door")
	if !ok {
		return ErrNoEntity
	}
	return c.sendWhenConnected(ctx, &api.CoverCommandRequest{
		Key:         key,
		HasPosition: true,
		Position:    position,
	})
}

func (c *Client) coverCommand(ctx context.Context, cmd api.LegacyCoverCommand) error {
	key, ok := c.entityKey("cover:door")
	if !ok {
		return ErrNoEntity
	}
	return c.sendWhenConnected(ctx, &api.CoverCommandRequest{
		Key:              key,
		HasLegacyCommand: true,
		LegacyCommand:    cmd,
	})
}

// TurnOnLight turns the opener light on.
func (c *Client) TurnOnLight(ctx context.Context) error { return c.lightCommand(ctx, true) }

// TurnOffLight turns the opener light off.
func (c *Client) TurnOffLight(ctx context.Context) error { return c.lightCommand(ctx, false) }

// ToggleLight flips the current light state.
func (c *Client) ToggleLight(ctx context.Context) error {
	s := c.State()
	return c.lightCommand(ctx, !s.Light)
}

func (c *Client) lightCommand(ctx context.Context, on bool) error {
	key, ok := c.entityKey("light:light")
	if !ok {
		return ErrNoEntity
	}
	return c.sendWhenConnected(ctx, &api.LightCommandRequest{
		Key:      key,
		HasState: true,
		State:    on,
	})
}

// Sync asks the device to re-query the opener for its current state. Useful
// if the ratgdo's state drifted from reality.
func (c *Client) Sync(ctx context.Context) error {
	return c.buttonPress(ctx, "button:sync")
}

// QueryStatus presses the "Query status" template button on the device.
func (c *Client) QueryStatus(ctx context.Context) error {
	return c.buttonPress(ctx, "button:query_status")
}

func (c *Client) buttonPress(ctx context.Context, id string) error {
	key, ok := c.entityKey(id)
	if !ok {
		return ErrNoEntity
	}
	return c.sendWhenConnected(ctx, &api.ButtonCommandRequest{Key: key})
}

// DeviceInfo queries the device for its identity metadata. Unlike the other
// commands, DeviceInfo blocks until a response arrives or ctx expires.
func (c *Client) DeviceInfo(ctx context.Context) (*DeviceInfo, error) {
	resp, err := c.exchange(ctx, &api.DeviceInfoRequest{}, api.DeviceInfoResponseTypeID)
	if err != nil {
		return nil, err
	}
	r := resp.(*api.DeviceInfoResponse)
	return &DeviceInfo{
		Name:            r.Name,
		Model:           r.Model,
		MACAddress:      r.MacAddress,
		ESPHomeVersion:  r.EsphomeVersion,
		CompilationTime: r.CompilationTime,
	}, nil
}

// DeviceInfo holds static metadata about a ratgdo device.
type DeviceInfo struct {
	Name, Model, MACAddress, ESPHomeVersion, CompilationTime string
}

// --- Internal helpers -----------------------------------------------------

func (c *Client) entityKey(id string) (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.entities[id]
	return k, ok
}

// sendWhenConnected waits for an active session (or ctx expiry / close) and
// then sends msg. Does not wait for any response.
func (c *Client) sendWhenConnected(ctx context.Context, msg proto.Message) error {
	conn, err := c.waitConnected(ctx)
	if err != nil {
		return err
	}
	return conn.Write(msg)
}

func (c *Client) waitConnected(ctx context.Context) (connection.ApiConnection, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrClosed
		}
		if c.connected {
			conn := c.apiConn
			c.mu.Unlock()
			return conn, nil
		}
		ch := c.stateCh
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
			// connection state changed; re-check.
		}
	}
}

// exchange is like sendWhenConnected but blocks for a response of the given
// type ID. Intended for request/response pairs like DeviceInfo.
func (c *Client) exchange(ctx context.Context, req proto.Message, respID uint64) (proto.Message, error) {
	conn, err := c.waitConnected(ctx)
	if err != nil {
		return nil, err
	}
	// The manager goroutine owns the read side, so we can't read directly.
	// Register a one-shot waiter before writing so we don't miss the reply.
	waiter := c.registerWaiter(respID)
	defer c.cancelWaiter(respID, waiter)
	if err := conn.Write(req); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-waiter:
		if msg == nil {
			return nil, ErrClosed
		}
		return msg, nil
	}
}

// One-shot waiter registration for request/response pairs (DeviceInfo etc).
// A single slot per type ID is enough for the handful of sync calls we make.
func (c *Client) registerWaiter(respID uint64) chan proto.Message {
	ch := make(chan proto.Message, 1)
	c.mu.Lock()
	if c.waiters == nil {
		c.waiters = map[uint64]chan proto.Message{}
	}
	// If another goroutine is already waiting, replace it; the old waiter
	// will drop its message and return nil when its own ctx expires. This
	// shouldn't happen in normal use since we only have DeviceInfo today.
	if old, ok := c.waiters[respID]; ok {
		close(old)
	}
	c.waiters[respID] = ch
	c.mu.Unlock()
	return ch
}

func (c *Client) cancelWaiter(respID uint64, ch chan proto.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, ok := c.waiters[respID]; ok && current == ch {
		delete(c.waiters, respID)
	}
}

func (c *Client) deliverWaiter(msg proto.Message) bool {
	id := api.TypeID(msg)
	c.mu.Lock()
	ch, ok := c.waiters[id]
	if ok {
		delete(c.waiters, id)
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- msg:
	default:
	}
	return true
}

// --- Connection lifecycle -------------------------------------------------

// connect runs the full session-setup sequence. On success, c.apiConn,
// c.entities, and initial state are populated and c.connected is true.
func (c *Client) connect(ctx context.Context) error {
	d := &net.Dialer{Timeout: c.timeout}
	tcpConn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}
	// Set a deadline covering the whole setup phase so any stalled read or
	// write aborts instead of hanging forever. Cleared before we hand the
	// conn off to the long-running read loop.
	if err := tcpConn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		_ = tcpConn.Close()
		return err
	}
	apiConn, err := connection.GetConnection(tcpConn, c.timeout, c.encKey)
	if err != nil {
		_ = tcpConn.Close()
		return err
	}
	if err := apiConn.Handshake(); err != nil {
		_ = tcpConn.Close()
		return fmt.Errorf("ratgdo: handshake: %w", err)
	}
	reader := bufio.NewReader(tcpConn)
	c.logger.Debug("ratgdo setup: handshake ok")

	// Hello. Announce an API version the server understands; the 1.10 pair
	// is what aioesphomeapi sends as of ESPHome 2026.4 (April 2026). Zeros
	// cause the server to disconnect_client_ immediately. Bump if a newer
	// ESPHome release stops accepting 1.10.
	if _, err := c.syncExchange(apiConn, reader,
		&api.HelloRequest{
			ClientInfo:      c.clientID,
			ApiVersionMajor: 1,
			ApiVersionMinor: 10,
		},
		api.HelloResponseTypeID); err != nil {
		_ = tcpConn.Close()
		return fmt.Errorf("ratgdo: hello: %w", err)
	}
	// Connect (login) is only required on plaintext sessions. With Noise
	// encryption the PSK authentication is already done in the handshake,
	// and sending ConnectRequest gets silently dropped by current ESPHome
	// servers.
	if c.encKey == "" {
		resp, err := c.syncExchange(apiConn, reader,
			&api.ConnectRequest{Password: ""},
			api.ConnectResponseTypeID)
		if err != nil {
			_ = tcpConn.Close()
			return fmt.Errorf("ratgdo: connect: %w", err)
		}
		if cr, ok := resp.(*api.ConnectResponse); ok && cr.InvalidPassword {
			_ = tcpConn.Close()
			return errors.New("ratgdo: connect: invalid password")
		}
	}
	// List entities.
	entities, err := c.syncListEntities(apiConn, reader)
	if err != nil {
		_ = tcpConn.Close()
		return fmt.Errorf("ratgdo: list entities: %w", err)
	}
	c.logger.Debug("ratgdo setup: listed entities", "count", len(entities))
	// Subscribe states (fire-and-forget; state messages flow in the read loop).
	if err := apiConn.Write(&api.SubscribeStatesRequest{}); err != nil {
		_ = tcpConn.Close()
		return fmt.Errorf("ratgdo: subscribe: %w", err)
	}
	// Clear setup deadline before the manage goroutine takes over reads.
	if err := tcpConn.SetDeadline(time.Time{}); err != nil {
		_ = tcpConn.Close()
		return err
	}

	c.mu.Lock()
	c.tcpConn = tcpConn
	c.apiConn = apiConn
	c.readerForManage = reader
	c.entities = entities
	c.connected = true
	// Wake waiters; they'll observe c.connected=true and return.
	close(c.stateCh)
	c.stateCh = make(chan struct{})
	state := c.state
	c.mu.Unlock()

	c.broadcast(Event{
		At:   time.Now(),
		Kind: EventConnected,
		Prev: state,
		Curr: state,
	})
	return nil
}

// manage runs the read loop and handles reconnects on drop.
func (c *Client) manage() {
	defer close(c.doneCh)
	delay := initialReconnectDelay
	// First iteration: we already have a live session from Dial's connect().
	first := true
	for {
		select {
		case <-c.closeCh:
			return
		default:
		}

		if !first {
			if err := c.connect(context.Background()); err != nil {
				c.logger.Warn("ratgdo reconnect failed", "addr", c.addr, "err", err)
				if sleepOrClose(c.closeCh, delay) {
					return
				}
				delay = minDuration(delay*2, maxReconnectDelay)
				continue
			}
			delay = initialReconnectDelay
		}
		first = false

		c.mu.Lock()
		apiConn := c.apiConn
		reader := c.readerForManage
		c.mu.Unlock()

		err := c.readLoop(apiConn, reader)

		c.markDisconnected()
		select {
		case <-c.closeCh:
			return
		default:
		}
		if err != nil && !errors.Is(err, io.EOF) && !isUseOfClosed(err) {
			c.logger.Info("ratgdo session ended", "err", err)
		}
		if sleepOrClose(c.closeCh, delay) {
			return
		}
		delay = minDuration(delay*2, maxReconnectDelay)
	}
}

func (c *Client) readLoop(apiConn connection.ApiConnection, reader *bufio.Reader) error {
	for {
		msg, err := apiConn.Read(reader)
		if err != nil {
			return err
		}
		// Deliver to any one-shot request waiter.
		c.deliverWaiter(msg)
		// Drive the client state machine.
		c.handleMessage(apiConn, msg)
	}
}

func (c *Client) handleMessage(apiConn connection.ApiConnection, msg proto.Message) {
	// Protocol-level keepalive and housekeeping.
	switch m := msg.(type) {
	case *api.PingRequest:
		_ = apiConn.Write(&api.PingResponse{})
	case *api.DisconnectRequest:
		_ = apiConn.Write(&api.DisconnectResponse{})
	case *api.GetTimeRequest:
		_ = apiConn.Write(&api.GetTimeResponse{EpochSeconds: uint32(time.Now().Unix())})
	case *api.CoverStateResponse:
		c.applyState(func(s *State) { applyCoverState(s, m) })
	case *api.LightStateResponse:
		c.applyState(func(s *State) { s.Light = m.State })
	case *api.BinarySensorStateResponse:
		c.applyBinarySensor(m.Key, m.State)
	case *api.SensorStateResponse:
		c.applySensor(m.Key, m.State)
	}

	// Always bump LastSeenAt on any message, even ones we don't otherwise
	// care about — proves the device is alive.
	c.mu.Lock()
	c.state.LastSeenAt = time.Now()
	c.mu.Unlock()
}

// applyState updates c.state under lock, computes prev/curr, and broadcasts
// a state-change event if anything visible actually changed.
func (c *Client) applyState(mutate func(*State)) {
	c.mu.Lock()
	prev := c.state
	mutate(&c.state)
	c.state.UpdatedAt = time.Now()
	c.state.LastSeenAt = c.state.UpdatedAt
	curr := c.state
	c.mu.Unlock()
	if statesEqualIgnoringTime(prev, curr) {
		return
	}
	c.broadcast(Event{At: curr.UpdatedAt, Kind: EventStateChange, Prev: prev, Curr: curr})
}

func (c *Client) applyBinarySensor(key uint32, state bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var field *bool
	switch {
	case c.entities["binary_sensor:motion"] == key:
		field = &c.state.Motion
	case c.entities["binary_sensor:obstruction"] == key:
		field = &c.state.Obstruction
	default:
		return
	}
	if *field == state {
		return
	}
	prev := c.state
	*field = state
	c.state.UpdatedAt = time.Now()
	c.state.LastSeenAt = c.state.UpdatedAt
	curr := c.state
	c.broadcastLocked(Event{At: curr.UpdatedAt, Kind: EventStateChange, Prev: prev, Curr: curr})
}

func (c *Client) applySensor(key uint32, value float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entities["sensor:openings"] != key {
		return
	}
	newVal := int(value)
	if c.state.Openings == newVal {
		return
	}
	prev := c.state
	c.state.Openings = newVal
	c.state.UpdatedAt = time.Now()
	c.state.LastSeenAt = c.state.UpdatedAt
	curr := c.state
	c.broadcastLocked(Event{At: curr.UpdatedAt, Kind: EventStateChange, Prev: prev, Curr: curr})
}

func applyCoverState(s *State, m *api.CoverStateResponse) {
	s.Position = m.Position
	switch m.CurrentOperation {
	case api.CoverOperation_COVER_OPERATION_IS_OPENING:
		s.Door = DoorOpening
	case api.CoverOperation_COVER_OPERATION_IS_CLOSING:
		s.Door = DoorClosing
	default: // IDLE
		switch {
		case m.Position <= 0.001:
			s.Door = DoorClosed
		case m.Position >= 0.999:
			s.Door = DoorOpen
		default:
			s.Door = DoorStopped
		}
	}
}

func statesEqualIgnoringTime(a, b State) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	a.LastSeenAt, b.LastSeenAt = time.Time{}, time.Time{}
	return a == b
}

func (c *Client) markDisconnected() {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return
	}
	c.connected = false
	if c.tcpConn != nil {
		_ = c.tcpConn.Close()
		c.tcpConn = nil
	}
	c.apiConn = nil
	c.readerForManage = nil
	// Signal waiters unless Close already closed stateCh on its way out.
	if !c.closed {
		close(c.stateCh)
		c.stateCh = make(chan struct{})
	}
	state := c.state
	// Flush any pending waiters for request/response (they'll see nil).
	for id, ch := range c.waiters {
		close(ch)
		delete(c.waiters, id)
	}
	c.mu.Unlock()

	c.broadcast(Event{
		At:   time.Now(),
		Kind: EventDisconnected,
		Prev: state,
		Curr: state,
	})
}

func (c *Client) broadcast(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broadcastLocked(ev)
}

func (c *Client) broadcastLocked(ev Event) {
	for _, ch := range c.subscribers {
		select {
		case ch <- ev:
		default:
			c.logger.Warn("ratgdo subscriber buffer full; dropping event")
		}
	}
}

// --- Synchronous request helpers used only during session setup ----------

// syncExchange writes a request and reads messages until one matches respID.
// Used during the setup handshake, before the read loop goroutine starts.
func (c *Client) syncExchange(apiConn connection.ApiConnection, reader *bufio.Reader,
	req proto.Message, respID uint64) (proto.Message, error) {
	if err := apiConn.Write(req); err != nil {
		return nil, err
	}
	for {
		msg, err := apiConn.Read(reader)
		if err != nil {
			return nil, err
		}
		if api.TypeID(msg) == respID {
			return msg, nil
		}
		// Handle housekeeping messages that can interleave.
		switch msg.(type) {
		case *api.PingRequest:
			_ = apiConn.Write(&api.PingResponse{})
		case *api.GetTimeRequest:
			_ = apiConn.Write(&api.GetTimeResponse{EpochSeconds: uint32(time.Now().Unix())})
		}
	}
}

// syncListEntities runs a ListEntitiesRequest and collects entity-key
// mappings until ListEntitiesDoneResponse arrives.
func (c *Client) syncListEntities(apiConn connection.ApiConnection, reader *bufio.Reader) (map[string]uint32, error) {
	if err := apiConn.Write(&api.ListEntitiesRequest{}); err != nil {
		return nil, err
	}
	entities := map[string]uint32{}
	for {
		msg, err := apiConn.Read(reader)
		if err != nil {
			return nil, err
		}
		switch m := msg.(type) {
		case *api.ListEntitiesDoneResponse:
			return entities, nil
		case *api.ListEntitiesCoverResponse:
			entities["cover:"+m.ObjectId] = m.Key
		case *api.ListEntitiesLightResponse:
			entities["light:"+m.ObjectId] = m.Key
		case *api.ListEntitiesBinarySensorResponse:
			entities["binary_sensor:"+m.ObjectId] = m.Key
		case *api.ListEntitiesSensorResponse:
			entities["sensor:"+m.ObjectId] = m.Key
		case *api.ListEntitiesSwitchResponse:
			entities["switch:"+m.ObjectId] = m.Key
		case *api.ListEntitiesButtonResponse:
			entities["button:"+m.ObjectId] = m.Key
		case *api.ListEntitiesLockResponse:
			entities["lock:"+m.ObjectId] = m.Key
		case *api.ListEntitiesNumberResponse:
			entities["number:"+m.ObjectId] = m.Key
		case *api.PingRequest:
			_ = apiConn.Write(&api.PingResponse{})
		case *api.GetTimeRequest:
			_ = apiConn.Write(&api.GetTimeResponse{EpochSeconds: uint32(time.Now().Unix())})
		}
	}
}

// --- Small helpers --------------------------------------------------------

func sleepOrClose(closeCh <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-closeCh:
		return true
	case <-t.C:
		return false
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func isUseOfClosed(err error) bool {
	// net.ErrClosed was added in Go 1.16.
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}
