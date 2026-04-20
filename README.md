# ratgdo-go

A Go client for [ratgdo](https://paulwieland.github.io/ratgdo/) garage-door
controllers running the upstream
[ESPHome firmware](https://github.com/ratgdo/esphome-ratgdo).

It speaks the ESPHome native API on TCP port 6053 with Noise-protocol
encryption. The device must be flashed with an `api.encryption.key`; the
matching base64 PSK is passed to `Dial`. Plaintext sessions are supported for
trusted networks by passing an empty key.

The entity schema is hardcoded for ratgdo boards — one cover entity named
`door`, one light, and the standard motion/obstruction/button sensors. Other
ESPHome devices will not work.

## Install

```
go get github.com/kevinburke/ratgdo-go
go install github.com/kevinburke/ratgdo-go/cmd/ratgdo@latest
```

## Library use

```go
ctx := context.Background()
client, err := ratgdo.Dial(ctx, "ratgdo.local:6053", os.Getenv("RATGDO_KEY"), nil)
if err != nil {
    log.Fatal(err)
}
defer client.Close()

if err := client.OpenDoor(ctx); err != nil {
    log.Fatal(err)
}

// React to state changes:
for ev := range client.Subscribe() {
    if ev.DoorFinishedClosing() {
        log.Printf("door closed at %s", ev.At)
    }
}
```

The `Client` is long-lived. After `Dial` returns it maintains the TCP session
in the background, reconnecting with exponential backoff whenever the
connection drops (device reboot, WiFi glitch, etc.). Commands issued while
disconnected block until reconnection or the caller's context expires.

`Client.State()` returns a snapshot of the most recent observed state.
`Client.Subscribe()` returns a channel that receives every state delta plus
connect/disconnect events. `Client.WaitFor(ctx, pred)` blocks until a
predicate over the state becomes true.

See the [package docs](https://pkg.go.dev/github.com/kevinburke/ratgdo-go)
for the full API.

## Command-line tool

`cmd/ratgdo` is a small CLI built on top of the library. Configure the
device address and encryption key via flags or environment variables:

```
export RATGDO_ADDRESS=ratgdo.local:6053
export RATGDO_KEY=<base64 PSK from the ESPHome config>

ratgdo info         # print device identity (model, MAC, ESPHome version)
ratgdo state        # print the current observed state and exit
ratgdo watch        # stream state changes until interrupted
ratgdo open
ratgdo close
ratgdo stop
ratgdo light-on
ratgdo light-off
```

Run `ratgdo --help` for the full flag list.

## Firmware

The stock ratgdo firmware exposes an unauthenticated HTTP API, and its
native ESPHome API has no encryption key by default. An example ESPHome
overlay that enables Noise-encrypted native API access — plus a `Makefile`
wrapping the ESPHome Docker image for compile/flash/logs — lives under
[`scripts/`](./scripts). See [`scripts/README.md`](./scripts/README.md) for
the full build-and-flash walkthrough.

## License

MIT
