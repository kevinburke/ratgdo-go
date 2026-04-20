// Command ratgdo is a small CLI around the ratgdo Go client. It was written
// mostly to exercise the library against a real device; no stability is
// promised.
//
// Subcommands:
//
//	info      Print the device's identity (ESPHome version, MAC, etc).
//	state     Print the current observed state once and exit.
//	watch     Stream state changes until interrupted.
//	open      Send an open command and exit.
//	close     Send a close command and exit.
//	stop      Send a stop command and exit.
//	light-on  Turn the opener light on.
//	light-off Turn the opener light off.
//
// Flags:
//
//	--address   Host:port of the ratgdo (default $RATGDO_ADDRESS)
//	--key       Base64 encryption key (default $RATGDO_KEY)
//	--timeout   Per-operation timeout (default 10s)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	ratgdo "github.com/kevinburke/ratgdo-go"
)

func main() {
	var (
		addr       = flag.String("address", os.Getenv("RATGDO_ADDRESS"), "ratgdo host:port (env RATGDO_ADDRESS)")
		key        = flag.String("key", os.Getenv("RATGDO_KEY"), "base64 API encryption key (env RATGDO_KEY)")
		timeout    = flag.Duration("timeout", 10*time.Second, "per-operation network timeout")
		printVer   = flag.Bool("version", false, "print version and exit")
		verbose    = flag.Bool("v", false, "verbose logging")
		watchLimit = flag.Duration("watch-duration", 0, "for 'watch': exit after this long (0 = forever)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <subcommand>\n\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nSubcommands: info, state, watch, open, close, stop, light-on, light-off\n")
	}
	flag.Parse()
	if *printVer {
		fmt.Println(ratgdo.Version)
		return
	}

	logLevel := slog.LevelWarn
	if *verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *addr == "" || *key == "" {
		fmt.Fprintln(os.Stderr, "error: --address and --key are required (or RATGDO_ADDRESS / RATGDO_KEY env vars)")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	dialCtx, dialCancel := context.WithTimeout(ctx, *timeout)
	client, err := ratgdo.Dial(dialCtx, *addr, *key, &ratgdo.Config{Timeout: *timeout})
	dialCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *addr, err)
		os.Exit(1)
	}
	defer client.Close()

	cmd := flag.Arg(0)
	switch cmd {
	case "info":
		runInfo(ctx, client)
	case "state":
		runState(client)
	case "watch":
		runWatch(ctx, client, *watchLimit)
	case "open":
		runCommand(ctx, client.OpenDoor)
	case "close":
		runCommand(ctx, client.CloseDoor)
	case "stop":
		runCommand(ctx, client.StopDoor)
	case "light-on":
		runCommand(ctx, client.TurnOnLight)
	case "light-off":
		runCommand(ctx, client.TurnOffLight)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		flag.Usage()
		os.Exit(2)
	}
}

func runInfo(ctx context.Context, c *ratgdo.Client) {
	info, err := c.DeviceInfo(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Name:            %s\n", info.Name)
	fmt.Printf("Model:           %s\n", info.Model)
	fmt.Printf("MAC:             %s\n", info.MACAddress)
	fmt.Printf("ESPHome version: %s\n", info.ESPHomeVersion)
	fmt.Printf("Compiled:        %s\n", info.CompilationTime)
}

func runState(c *ratgdo.Client) {
	// Give the initial state dump ~1s to arrive after Dial.
	time.Sleep(750 * time.Millisecond)
	s := c.State()
	fmt.Printf("Door:        %s (pos %.2f)\n", s.Door, s.Position)
	fmt.Printf("Light:       %v\n", s.Light)
	fmt.Printf("Motion:      %v\n", s.Motion)
	fmt.Printf("Obstruction: %v\n", s.Obstruction)
	fmt.Printf("Openings:    %d\n", s.Openings)
	fmt.Printf("LastSeen:    %s\n", s.LastSeenAt.Format(time.RFC3339))
}

func runWatch(ctx context.Context, c *ratgdo.Client, limit time.Duration) {
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	events := c.Subscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			switch ev.Kind {
			case ratgdo.EventConnected:
				fmt.Printf("[%s] connected\n", ev.At.Format(time.TimeOnly))
			case ratgdo.EventDisconnected:
				fmt.Printf("[%s] disconnected\n", ev.At.Format(time.TimeOnly))
			case ratgdo.EventStateChange:
				describeDelta(ev)
			}
		}
	}
}

func describeDelta(ev ratgdo.Event) {
	ts := ev.At.Format(time.TimeOnly)
	switch {
	case ev.DoorStartedOpening():
		fmt.Printf("[%s] door opening\n", ts)
	case ev.DoorFinishedOpening():
		fmt.Printf("[%s] door open\n", ts)
	case ev.DoorStartedClosing():
		fmt.Printf("[%s] door closing\n", ts)
	case ev.DoorFinishedClosing():
		fmt.Printf("[%s] door closed\n", ts)
	case ev.Prev.Light != ev.Curr.Light:
		fmt.Printf("[%s] light %v → %v\n", ts, ev.Prev.Light, ev.Curr.Light)
	case ev.Prev.Motion != ev.Curr.Motion:
		if ev.Curr.Motion {
			fmt.Printf("[%s] motion detected\n", ts)
		}
	case ev.Prev.Obstruction != ev.Curr.Obstruction:
		fmt.Printf("[%s] obstruction: %v\n", ts, ev.Curr.Obstruction)
	case ev.OpeningsIncreased():
		fmt.Printf("[%s] openings: %d → %d\n", ts, ev.Prev.Openings, ev.Curr.Openings)
	}
}

func runCommand(ctx context.Context, fn func(context.Context) error) {
	if err := fn(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
