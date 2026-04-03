# IoT Automation Notification Service

A lightweight, declarative notification engine for your smart home, written in Go. It listens to NATS events and sends notifications through various channels like Home Assistant, Google Home, and smart TVs.

## Features

- **Event-Driven**: Subscribes to `iot.v1.events.>` on NATS JetStream.
- **Declarative Configuration**: Define automation rules in a simple `config.yaml`.
- **Mute Functionality**: Automatically creates MQTT discovery switches in Home Assistant to mute/unmute specific notification rules.
- **Persistent State**: Mute status is stored in NATS Key-Value store (`iot_state`).
- **Multi-Channel Support**:
    - **Home Assistant**: Push notifications to mobile apps.
    - **Google Home**: TTS support (Stub).
    - **TV**: Screen toast notifications (Stub).

## Configuration

The service uses a `config.yaml` to define rules. Here is an example:

```yaml
notifications:
  - id: "doorbell"
    trigger:
      devices: ["binary_sensor.front_door_bell"]
      type: "binary_sensor"
      state: "ON"
    conditions:
      - device: "lock.night_mode"
        operator: "=="
        value: "false"
        default: "false"
    actions:
      - channel: "mobile_app"
        target: "dennis_iphone"
        title: "Klingel!"
        message: "Es hat an der Tür geklingelt."
      - channel: "google_home"
        target: "living_room_speaker"
        message: "Jemand ist an der Tür."
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://nats.event-bus:4222` | NATS Server URL |
| `MQTT_BROKER` | `tcp://mosquitto.iot:1883` | MQTT Broker URL |
| `CLIENT_ID` | `iot-notification-service` | Unique MQTT Client ID |
| `CONFIG_PATH` | `/etc/iot/config.yaml` | Path to the configuration file |

## Deployment (Kubernetes)

This service is designed to run in a Kubernetes cluster (e.g., in the `iot` namespace). Ensure it has access to both NATS and Mosquitto.

```yaml
# Example deployment snippet
apiVersion: apps/v1
kind: Deployment
metadata:
  name: iot-automation-notification
  namespace: iot
spec:
  template:
    spec:
      containers:
        - name: service
          image: ghcr.io/dennisschroeder/iot-automation-notification:latest
          args: ["--config", "/config/config.yaml"]
          volumeMounts:
            - name: config
              mountPath: /config
      volumes:
        - name: config
          configMap:
            name: iot-notification-config
```

## Development

Requires Go 1.24.

```bash
go mod download
go run main.go --config local_config.yaml
```

## License

MIT
