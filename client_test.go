package ratgdo

import (
	"bufio"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mycontroller-org/esphome_api/pkg/api"
	"google.golang.org/protobuf/proto"
)

type fakeAPIConnection struct {
	writes chan proto.Message
}

func (f *fakeAPIConnection) Write(message proto.Message) error {
	f.writes <- message
	return nil
}

func (f *fakeAPIConnection) Read(*bufio.Reader) (proto.Message, error) {
	return nil, errors.New("unexpected fakeAPIConnection.Read call")
}

func (f *fakeAPIConnection) Handshake() error {
	return nil
}

func TestListEntityObjectID(t *testing.T) {
	cases := []struct {
		name     string
		objectID string
		entity   string
		want     string
	}{
		{name: "uses provided object id", objectID: "door", entity: "Door", want: "door"},
		{name: "lowercases and underscores spaces", entity: "Query Status", want: "query_status"},
		{name: "preserves dashes", entity: "Side-Light", want: "side-light"},
		{name: "sanitizes punctuation", entity: "Door/Status", want: "door_status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := listEntityObjectID(tc.objectID, tc.entity); got != tc.want {
				t.Fatalf("listEntityObjectID(%q, %q) = %q, want %q", tc.objectID, tc.entity, got, tc.want)
			}
		})
	}
}

func TestPingWritesRequestAndWaitsForResponse(t *testing.T) {
	conn := &fakeAPIConnection{writes: make(chan proto.Message, 1)}
	c := &Client{
		apiConn:   conn,
		connected: true,
		stateCh:   make(chan struct{}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Ping(context.Background())
	}()

	select {
	case msg := <-conn.writes:
		if _, ok := msg.(*api.PingRequest); !ok {
			t.Fatalf("Ping wrote %T, want *api.PingRequest", msg)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Ping to write request")
	}

	if delivered := c.deliverWaiter(&api.PingResponse{}); !delivered {
		t.Fatalf("deliverWaiter returned false for PingResponse")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Ping returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Ping to return")
	}
}

func TestPingReturnsErrClosedOnDisconnect(t *testing.T) {
	conn := &fakeAPIConnection{writes: make(chan proto.Message, 1)}
	c := &Client{
		apiConn:   conn,
		connected: true,
		stateCh:   make(chan struct{}),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Ping(context.Background())
	}()

	select {
	case <-conn.writes:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Ping to write request")
	}

	c.markDisconnected()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("Ping returned %v, want %v", err, ErrClosed)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Ping to return")
	}
}
