# Custom ratgdo firmware

These files build an ESPHome firmware for a ratgdo v3.2 board (ESP32,
Security+ 2.0) with the native API enabled over Noise encryption, so the
Go client at <https://github.com/kevinburke/ratgdo-go> can talk to it.

They differ from the stock firmware at <https://ratgdo.github.io/esphome-ratgdo/>
in two ways:

1. **Native ESPHome API on TCP 6053 with a Noise-protocol encryption key.**
   Stock firmware ships with an unauthenticated HTTP API on port 80 —
   anyone on the network could open the door with a single POST.
2. **HTTP basic auth in front of the web UI**, so the still-available HTTP
   endpoints can't be invoked without credentials.

Neither of those replaces a network ACL / IoT VLAN firewall — they're
defense in depth.

Layout:

```
scripts/
├── README.md             ← this file
├── Makefile              ← wraps the ESPHome Docker image
├── ratgdo32.yaml         ← ESPHome overlay on top of upstream v32board.yaml
└── secrets.yaml.example  ← template; copy to secrets.yaml
```

## Prerequisites

- Docker. Every action runs `ghcr.io/esphome/esphome` in a short-lived
  container; there's no long-running ESPHome dashboard or Home Assistant.
- `openssl` for key generation.
- `curl` for the one-time initial flash from stock firmware.
- Network reachability to the device on TCP 80 (initial web OTA) and
  TCP 3232 (ESPHome-native OTA afterwards).

## One-time setup

```bash
cd scripts
cp secrets.yaml.example secrets.yaml
make genkey            # prints a 32-byte base64 PSK; paste into secrets.yaml
                       # as api_encryption_key
$EDITOR secrets.yaml   # fill in wifi_ssid, wifi_password, web_username,
                       # web_password
```

Save the `api_encryption_key` somewhere durable (password manager). Without
it the Go client can't reach the device, and the only way back in is a
serial-over-USB reflash.

## Initial flash (stock firmware → custom firmware)

The stock firmware's ESPHome-native OTA on port 3232 is typically disabled,
so the first cutover goes through the `web_server` `/update` endpoint, which
is unauthenticated on the stock image. Subsequent flashes use `make upload`.

```bash
cd scripts
make compile

# Find the OTA binary (path may vary by ESPHome version).
# Use .ota.bin (not .factory.bin) for the /update endpoint — the device's
# bootloader is already in place, so we only want the OTA-sized image.
BIN=$(find .esphome/build -name '*.ota.bin' | head -1)
test -n "$BIN" || { echo "no .ota.bin found"; exit 1; }

# Push it. Stock firmware's /update has no auth yet; ours will.
# Replace <device> with the IP or hostname of your ratgdo.
curl --fail --show-error \
     --form "file=@$BIN" \
     http://<device>/update
```

The device reboots. Within ~30s:

- `curl http://<device>/` should now prompt for HTTP basic auth.
- `nc -z -G 2 <device> 6053 && echo ok` should print `ok`.

If you bricked it (no response on port 80 or 6053 after 2 min), see
"Recovery" below.

## Subsequent flashes

Once the device is running the custom firmware, ESPHome-native OTA is the
happy path:

```bash
cd scripts
make upload DEVICE=<device>
```

`make upload` compiles if needed, connects to `<device>:3232`, and pushes
the new firmware. The device reboots automatically.

To stream logs while iterating:

```bash
make logs DEVICE=<device>
```

## Verifying the native API

Use the `ratgdo` CLI that ships with this repo:

```bash
export RATGDO_ADDRESS=<device>:6053
export RATGDO_KEY=$(awk '/^api_encryption_key:/ {print $2}' secrets.yaml | tr -d '"')

go run github.com/kevinburke/ratgdo-go/cmd/ratgdo info
```

You should see the device name, model, and ESPHome version. If you get a
handshake error, the key doesn't match what's baked into the firmware.

## Rotating the encryption key

1. `make genkey`, paste the new value over `api_encryption_key` in
   `secrets.yaml`.
2. `make upload DEVICE=<device>`. Device reboots with the new key.
3. Update any client (the Go client, any scripts) with the new key.

Order matters: if you update a client first it can't reach the device until
the flash completes.

## Recovery (serial reflash)

If OTA is broken, unplug the device, connect it to a computer with a USB-C
cable, and open <https://ratgdo.github.io/esphome-ratgdo/>. The web
installer uses WebSerial to write the stock firmware. After that, redo
"Initial flash" above.

## References

- Upstream ESPHome config: <https://github.com/ratgdo/esphome-ratgdo>
  (specifically `v32board.yaml` and `base.yaml`)
- ESPHome native API encryption:
  <https://esphome.io/components/api.html#configuration-variables>
