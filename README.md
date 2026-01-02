# Alertmanager-Webhook-MQTT-Bridge

Go microservice that accepts Alertmanager webhook v2 payloads and publishes a retained MQTT status message representing the highest active alert severity.

## Configuration

Environment variables:

```
HTTP_LISTEN_ADDR=:8080
MQTT_BROKER=tcp://mosquitto:1883
MQTT_TOPIC=homelab/health
MQTT_CLIENT_ID=alertmanager-mqtt-bridge
MQTT_USERNAME=your-user
MQTT_PASSWORD=your-pass
```

## HTTP

- `POST /alert` with `Content-Type: application/json` (Alertmanager webhook v2 schema)

## MQTT

- QoS 1, retained
- Payload (JSON):

```json
{
  "state": "CRITICAL",
  "active_alerts": 3,
  "source": "alertmanager"
}
```

## Nix

Build (first build will print the required `vendorHash`):

```
nix build
```

Run:

```
nix run
```
