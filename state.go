package ratgdo

import "time"

// DoorOp is the door's motion state as reported by the opener.
type DoorOp int

const (
	// DoorUnknown is the zero value, used before the first state event arrives.
	DoorUnknown DoorOp = iota
	DoorClosed
	DoorOpen
	DoorOpening
	DoorClosing
	// DoorStopped means the door was halted mid-travel and is neither fully
	// open nor fully closed.
	DoorStopped
)

func (d DoorOp) String() string {
	switch d {
	case DoorClosed:
		return "closed"
	case DoorOpen:
		return "open"
	case DoorOpening:
		return "opening"
	case DoorClosing:
		return "closing"
	case DoorStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// State is a snapshot of the ratgdo's observable state. All fields reflect
// the last value received from the device.
type State struct {
	Door     DoorOp
	Position float32 // 0 = fully closed, 1 = fully open
	Light    bool
	// Motion is true while the opener's motion sensor is active. It
	// typically auto-resets after a few seconds on the device.
	Motion      bool
	Obstruction bool
	// Openings is the lifetime count of door openings, persisted on-device
	// across reboots.
	Openings int
	// UpdatedAt is the time we last observed any state change. Zero before
	// the first state event.
	UpdatedAt time.Time
	// LastSeenAt is the time we last received any message from the device,
	// including pings and unchanged state updates. Zero until first message.
	LastSeenAt time.Time
}

// EventKind distinguishes state deltas from connection-lifecycle events.
type EventKind int

const (
	// EventStateChange is emitted whenever any field of State differs from
	// the previous State.
	EventStateChange EventKind = iota
	// EventConnected is emitted after the initial Dial returns and after
	// every successful reconnection.
	EventConnected
	// EventDisconnected is emitted when the background session drops. A
	// reconnect attempt follows automatically unless Close was called.
	EventDisconnected
)

func (k EventKind) String() string {
	switch k {
	case EventStateChange:
		return "state-change"
	case EventConnected:
		return "connected"
	case EventDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// Event is a change notification delivered on the channel returned by
// Client.Subscribe. For EventStateChange, Prev and Curr show the delta. For
// connection events, Prev and Curr are equal (both hold the most recent
// known state).
type Event struct {
	At   time.Time
	Kind EventKind
	Prev State
	Curr State
}

// DoorStartedOpening reports whether this event represents the door
// transitioning from closed or stopped into the opening state.
func (e Event) DoorStartedOpening() bool {
	return e.Kind == EventStateChange && e.Prev.Door != DoorOpening && e.Curr.Door == DoorOpening
}

// DoorStartedClosing reports the door transitioning into the closing state.
func (e Event) DoorStartedClosing() bool {
	return e.Kind == EventStateChange && e.Prev.Door != DoorClosing && e.Curr.Door == DoorClosing
}

// DoorFinishedOpening reports the door reaching the fully-open state.
func (e Event) DoorFinishedOpening() bool {
	return e.Kind == EventStateChange && e.Prev.Door != DoorOpen && e.Curr.Door == DoorOpen
}

// DoorFinishedClosing reports the door reaching the fully-closed state.
func (e Event) DoorFinishedClosing() bool {
	return e.Kind == EventStateChange && e.Prev.Door != DoorClosed && e.Curr.Door == DoorClosed
}

// OpeningsIncreased reports whether the lifetime opening counter advanced
// in this event. Useful for detecting activity missed during a disconnect:
// a single EventConnected can report many openings if the gap was long.
func (e Event) OpeningsIncreased() bool {
	return e.Curr.Openings > e.Prev.Openings
}
