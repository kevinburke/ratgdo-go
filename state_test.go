package ratgdo

import "testing"

func TestEventDoorTransitions(t *testing.T) {
	cases := []struct {
		name                                                            string
		prev, curr                                                      DoorOp
		startedOpen, startedClose, finishedOpen, finishedClose, noState bool
	}{
		{"closed→opening", DoorClosed, DoorOpening, true, false, false, false, false},
		{"opening→open", DoorOpening, DoorOpen, false, false, true, false, false},
		{"open→closing", DoorOpen, DoorClosing, false, true, false, false, false},
		{"closing→closed", DoorClosing, DoorClosed, false, false, false, true, false},
		{"no change", DoorOpen, DoorOpen, false, false, false, false, false},
		{"stopped→closing", DoorStopped, DoorClosing, false, true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := Event{
				Kind: EventStateChange,
				Prev: State{Door: tc.prev},
				Curr: State{Door: tc.curr},
			}
			if got := ev.DoorStartedOpening(); got != tc.startedOpen {
				t.Errorf("DoorStartedOpening: got %v want %v", got, tc.startedOpen)
			}
			if got := ev.DoorStartedClosing(); got != tc.startedClose {
				t.Errorf("DoorStartedClosing: got %v want %v", got, tc.startedClose)
			}
			if got := ev.DoorFinishedOpening(); got != tc.finishedOpen {
				t.Errorf("DoorFinishedOpening: got %v want %v", got, tc.finishedOpen)
			}
			if got := ev.DoorFinishedClosing(); got != tc.finishedClose {
				t.Errorf("DoorFinishedClosing: got %v want %v", got, tc.finishedClose)
			}
		})
	}
}

func TestEventOpeningsIncreased(t *testing.T) {
	ev := Event{Prev: State{Openings: 5}, Curr: State{Openings: 7}}
	if !ev.OpeningsIncreased() {
		t.Fatalf("expected OpeningsIncreased to be true for 5→7")
	}
	ev = Event{Prev: State{Openings: 5}, Curr: State{Openings: 5}}
	if ev.OpeningsIncreased() {
		t.Fatalf("expected OpeningsIncreased to be false for 5→5")
	}
}

func TestEventHelpersIgnoreNonStateKinds(t *testing.T) {
	// Connect/Disconnect events carry Prev==Curr and should not look like
	// door transitions even if Door field is set.
	ev := Event{Kind: EventConnected, Prev: State{Door: DoorClosed}, Curr: State{Door: DoorClosed}}
	if ev.DoorStartedOpening() || ev.DoorFinishedOpening() ||
		ev.DoorStartedClosing() || ev.DoorFinishedClosing() {
		t.Fatalf("connect/disconnect events should not register as transitions")
	}
}
