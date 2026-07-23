# CLAUDE.md

Guidance for AI assistants (and humans) working in this repository.

## Overview

`iot-automation-notification` is a lightweight, declarative **notification engine**
for a self-hosted smart home, written in Go. It listens to IoT device events on
**NATS JetStream**, evaluates user-defined rules from a `config.yaml`, and
dispatches notifications through pluggable channels (Home Assistant mobile push,
Google Home TTS, Music Assistant announcements, TV toasts).

It is a companion service to a broader homelab IoT platform: device events are
published as Protobuf messages (schemas come from the private module
`github.com/dennisschroeder/iot-schemas-proto`), and this service reacts to them.
It runs as a single container in Kubernetes (typically the `iot` namespace).

## Architecture

The flow is: **NATS event → rule match → debounce → mute check → conditions → fan-out to provider channels.**

```
main.go
  └─ cmd/root.go            Cobra root command; parses flags/env, wires clients + service
       ├─ internal/config           Loads & normalizes config.yaml (rules)
       ├─ internal/transport/nats   NATS JetStream connection + KV store (iot_state bucket)
       ├─ internal/transport/mqtt   Paho MQTT client (HA discovery + service calls)
       ├─ internal/logic/service    Core engine: subscribe, match, evaluate, dispatch
       └─ internal/provider         NotificationProvider implementations (channels)
```

### Key packages

- **`cmd/root.go`** — the only Cobra command. All configuration is via persistent
  flags (which double as env-configurable knobs). Sets up JSON structured logging
  via `slog`, constructs the NATS and MQTT clients, then builds and runs the `Service`.

- **`internal/config/rules.go`** — YAML schema and `LoadConfig`. A `Config` holds a
  list of `NotificationRule`s, each with a `Trigger`, optional `Conditions`, and a
  list of `Actions`. `LoadConfig` normalizes the trigger (accepts either a single
  `device` or a `devices` array; defaults `type` to `binary_sensor`).

- **`internal/logic/service.go`** — the heart of the service (`Service`). It:
  1. Optionally starts an HTTP file server on `:8080` under `/cache/` to serve
     cached TTS `.wav` files (only when `--cache-dir` is set).
  2. Sets up **mute switches**: for every rule it publishes a Home Assistant MQTT
     discovery `switch`, seeds/reads mute state from NATS KV, and listens for
     `set` commands to toggle mute. Mute state is stored in KV under `lock.<rule.id>`
     (`"true"` = muted).
  3. Subscribes to `iot.v1.events.>`, unmarshals the Protobuf `EventEnvelope`, and
     routes binary-sensor and light events to handlers.
  4. `evaluateAndExecute` applies, in order: **debounce** (10s per rule id),
     **mute/lock check** (NATS KV `lock.<id>`), **conditions**, then fans out
     `Actions` to providers concurrently (one goroutine per action, `WaitGroup`).

- **`internal/provider/provider.go`** — the `NotificationProvider` interface
  (`Send(ctx, action) error` + `Name() string`) and three implementations:
  - `HomeAssistantProvider` (`Name() == "mobile_app"`): publishes a Protobuf
    `ActionRequest` to `iot.v1.actions.notification.<target>` on NATS. A separate
    bridge translates this into an HA `notify.mobile_app_<target>` service call.
  - `GoogleHomeProvider` (`Name() == "google_home"`): TTS via MQTT service calls;
    tracks per-player volume from `homeassistant/media_player/+/volume_level` and
    bumps volume to 0.6 before speaking if too low.
  - `TVProvider` (`Name() == "tv"`): stub, logs only.

- **`internal/provider/music_assistant.go`** — `MusicAssistantProvider`
  (`Name() == "music_assistant"`), only registered when `--mass-url` is set. It:
  1. Synthesizes speech locally via a **Piper** TTS server using the **Wyoming
     protocol** (`synthesizeSpeech` over a raw TCP connection), wraps the raw PCM
     in a WAV header (`addWavHeader`), and caches it to disk keyed by
     `sha256(message + version-suffix)`.
  2. Serves the cached file via the callback URL and calls the Music Assistant
     JSON-RPC API (`players/cmd/play_announcement`) to play it, falling back to
     MAS's internal `tts://` provider if local synthesis is unavailable.

- **`internal/transport/nats/client.go`** — thin wrapper over `nats.go`: pub/sub
  plus a Key-Value helper bound to the `iot_state` bucket (used for mute/lock state).

- **`internal/transport/mqtt/client.go`** — thin wrapper over Paho MQTT:
  `PublishDiscovery` (retained config topics under `homeassistant/...`), `Publish`,
  and `Subscribe`. Uses a persistent session (`CleanSession(false)`, `ResumeSubs`).

## Configuration

Rules live in a YAML file (`--config`, default `/etc/iot/config.yaml`). Example:

```yaml
notifications:
  - id: "doorbell"                       # also the mute-switch id (lock.doorbell in KV)
    trigger:
      devices: ["binary_sensor.front_door_bell"]
      type: "binary_sensor"              # "binary_sensor" | "light"
      state: "ON"                        # "ON" | "OFF"
    conditions:
      - device: "lock.night_mode"        # KV key (or entity id) to read
        operator: "=="                   # "==", "!=", "<", ">", "<=", ">="
        value: "false"
        default: "false"                 # fallback if key missing from KV
    actions:
      - channel: "mobile_app"            # provider Name(): mobile_app | google_home | music_assistant | tv
        target: "dennis_iphone"
        title: "Klingel!"
        message: "Es hat an der Tür geklingelt."
      - channel: "google_home"
        target: "living_room_speaker"
        message: "Jemand ist an der Tür."
```

Notes for editing rules/config code:
- `Condition.Type == "time"` is a **stub** (always true) — real time logic is not implemented.
- Trigger matching accepts an exact `entity_id` match **or** a suffix match on `.<device-name>`.
- Device entity ids arrive from Protobuf events; states are `common.BinaryState` enums
  mapped to the strings `"ON"`/`"OFF"`.

## Runtime configuration (flags / env)

All configuration is via Cobra flags on the root command. The README documents an
env-var mapping (`NATS_URL`, `MQTT_BROKER`, `CLIENT_ID`, `CONFIG_PATH`); in code the
canonical source is the flags in `cmd/root.go`:

| Flag | Default | Purpose |
|------|---------|---------|
| `--nats-url` | `nats://nats.event-bus:4222` | NATS server |
| `--mqtt-broker` | `tcp://mosquitto.iot:1883` | MQTT broker |
| `--client-id` | `iot-notification-service` | MQTT client id |
| `--log-level` | `info` | `debug`\|`info`\|`warn`\|`error` |
| `--config` | `/etc/iot/config.yaml` | rules file path |
| `--google-homes` | `""` | comma-separated Google Home media_players |
| `--mass-url` | `""` | Music Assistant server URL (enables MAS provider) |
| `--mass-token` | `""` | MAS long-lived bearer token |
| `--piper-url` | `""` | Piper TTS `host:port` (Wyoming protocol) |
| `--cache-dir` | `""` | dir for cached TTS `.wav` (enables `:8080` cache server) |
| `--callback-url` | `""` | externally reachable URL MAS uses to fetch cached audio |

## Development

Requires **Go 1.24**.

```bash
go mod download
go build ./...
go vet ./...
go test ./...
go run . --config test_config.yaml --log-level debug   # local run
```

- The module depends on the **private** module `iot-schemas-proto`. Local builds
  need `GOPRIVATE=github.com/dennisschroeder/*` and git credentials for it; the
  Docker build injects a `GH_PAT` build-arg to fetch it.
- `main_test.go` is an exploratory/scratch test (`TestPayload`) that prints a
  candidate MAS JSON-RPC payload — it does not assert behavior and its payload
  shape differs from the current production code. Don't treat it as a spec.
- There is no lint config beyond `go vet`; keep to standard `gofmt` formatting.

## Build & release

- **`Dockerfile`**: multi-stage, multi-arch (`$TARGETOS`/`$TARGETARCH`), builds a
  static binary (`CGO_ENABLED=0`, `-ldflags="-w -s"`) into a `scratch` image.
- **`.github/workflows/release.yml`**: on push to `main`, auto-bumps a semver tag
  (default bump: `patch`), creates a GitHub release, and builds/pushes a
  multi-arch image to `ghcr.io/dennisschroeder/iot-automation-notification`
  (`:latest` and `:<version>`). **Merging to `main` triggers a real release** —
  be deliberate about what lands there.

## Conventions

- **Logging**: always `log/slog` with structured key/value pairs (JSON handler).
  Match the existing style: `slog.Info("message", "key", value, ...)`.
- **Commit messages**: Conventional Commits with a scope, e.g.
  `fix(mas): ...`, `feat(mas): ...`, `chore: ...`. `mas` = Music Assistant.
- **Error handling**: wrap with `fmt.Errorf("...: %w", err)`; providers return
  errors and the engine logs them per-action without aborting other actions.
- **Concurrency**: shared maps on `Service` (`lastFired`) and the volume map on
  `GoogleHomeProvider` are mutex-guarded — keep new shared state guarded too.
- **New notification channel**: implement `provider.NotificationProvider`, give it
  a unique `Name()`, and register it in `logic.NewService`. The `channel` field in
  a rule's action must equal that `Name()`.

## Git workflow

- Do not push directly to `main` unless intentionally cutting a release (it triggers
  the release/publish pipeline).
- Do not commit secrets (`GH_PAT`, `--mass-token`), cached audio, or local config.
