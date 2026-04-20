// Package ratgdo is a Go client for ratgdo garage-door controllers running
// the upstream ESPHome firmware (https://github.com/ratgdo/esphome-ratgdo).
//
// It speaks the ESPHome native API on TCP port 6053 with Noise-protocol
// encryption. The device must be flashed with an api.encryption.key; the
// matching base64 PSK is passed to Dial.
//
// The Client is long-lived. After a successful Dial it maintains the TCP
// session in the background, reconnecting with exponential backoff whenever
// the connection drops (device reboot, WiFi glitch, etc.). Commands issued
// while disconnected block until reconnection or the caller's context
// expires.
//
// The entity schema is hardcoded for ratgdo boards — one cover entity named
// "door", one light, and the standard motion/obstruction/motor/button
// sensors. Other ESPHome devices will not work with this package.
package ratgdo
