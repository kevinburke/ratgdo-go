# Build and flash custom ratgdo firmware.
#
# All actions run ESPHome inside a short-lived Docker container — there is no
# dashboard or daemon to keep running. See README.md for end-to-end
# instructions (initial flash, key rotation, etc).

SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DELETE_ON_ERROR:

# Override on the command line or in the environment, e.g.
#     make upload DEVICE=ratgdo.local
DEVICE          ?=
CONFIG          ?= ratgdo32.yaml
ESPHOME_VERSION ?= stable
ESPHOME_IMAGE   ?= ghcr.io/esphome/esphome:$(ESPHOME_VERSION)

# No --network host: that is broken on macOS Docker Desktop, and we only need
# outbound TCP to the device (which works with the default bridge network).
DOCKER := docker run --rm \
  -v "$(CURDIR):/config" \
  -w /config \
  $(ESPHOME_IMAGE)

.PHONY: help
help:
	@echo "Targets:"
	@echo "  compile    Compile $(CONFIG); binary lands under .esphome/build/"
	@echo "  upload     OTA-upload to \$$DEVICE over ESPHome native OTA (TCP 3232)"
	@echo "  logs       Tail device logs over the native API"
	@echo "  genkey     Print a fresh base64 32-byte API encryption key"
	@echo "  clean      Remove ESPHome build cache"
	@echo ""
	@echo "Set DEVICE to the device's address, e.g.:"
	@echo "    make upload DEVICE=ratgdo.local"
	@echo ""
	@echo "First-time flash (from stock firmware) uses a curl POST to the"
	@echo "device's /update endpoint — see README.md."

.PHONY: compile
compile: secrets.yaml
	$(DOCKER) compile $(CONFIG)

.PHONY: upload
upload: secrets.yaml require-device
	$(DOCKER) upload $(CONFIG) --device $(DEVICE)

.PHONY: logs
logs: secrets.yaml require-device
	$(DOCKER) logs $(CONFIG) --device $(DEVICE)

.PHONY: genkey
genkey:
	@openssl rand -base64 32

.PHONY: clean
clean:
	rm -rf .esphome

.PHONY: require-device
require-device:
	@if [ -z "$(DEVICE)" ]; then \
	  echo "ERROR: set DEVICE=<host-or-ip>, e.g. make upload DEVICE=ratgdo.local"; \
	  exit 1; \
	fi

# Fail fast with a useful message if the operator forgot to create secrets.yaml.
secrets.yaml:
	@echo "ERROR: secrets.yaml is missing."
	@echo "       cp secrets.yaml.example secrets.yaml and fill it in"
	@echo "       (use 'make genkey' to mint a fresh api_encryption_key)"
	@exit 1
