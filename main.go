package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type webhookPayload struct {
	Alerts []alert `json:"alerts"`
}

type alert struct {
	Status string            `json:"status"`
	Labels map[string]string `json:"labels"`
}

type mqttMessage struct {
	State        string `json:"state"`
	ActiveAlerts int    `json:"active_alerts"`
	Source       string `json:"source"`
}

var severityRank = map[string]int{
	"ok":       0,
	"info":     1,
	"warning":  2,
	"error":    3,
	"critical": 4,
}

func main() {
	listenAddr := getEnv("HTTP_LISTEN_ADDR", ":8080")
	broker := getEnv("MQTT_BROKER", "tcp://mosquitto:1883")
	topic := getEnv("MQTT_TOPIC", "homelab/health")
	clientID := getEnv("MQTT_CLIENT_ID", "alertmanager-mqtt-bridge")

	client := connectMQTT(broker, clientID)

	http.HandleFunc("/alert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}

		var payload webhookPayload
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}

		state, active := highestSeverity(payload.Alerts)
		if err := publishState(client, topic, state, active); err != nil {
			log.Printf("mqtt publish failed: %v", err)
			http.Error(w, "failed to publish", http.StatusBadGateway)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	log.Printf("listening on %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("http server stopped: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func connectMQTT(broker, clientID string) mqtt.Client {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID(clientID)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(2 * time.Second)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if token.Wait() && token.Error() != nil {
		log.Fatalf("mqtt connect failed: %v", token.Error())
	}
	return client
}

func highestSeverity(alerts []alert) (string, int) {
	active := 0
	highest := "ok"
	highestRank := severityRank[highest]

	for _, a := range alerts {
		if a.Status != "firing" {
			continue
		}
		active++
		severity := "info"
		if a.Labels != nil {
			if s := strings.ToLower(strings.TrimSpace(a.Labels["severity"])); s != "" {
				severity = s
			}
		}
		rank, ok := severityRank[severity]
		if !ok {
			rank = severityRank["info"]
		}
		if rank > highestRank {
			highestRank = rank
			highest = severity
		}
	}

	return strings.ToUpper(highest), active
}

func publishState(client mqtt.Client, topic, state string, active int) error {
	payload, err := json.Marshal(mqttMessage{
		State:        state,
		ActiveAlerts: active,
		Source:       "alertmanager",
	})
	if err != nil {
		return err
	}

	token := client.Publish(topic, 1, true, payload)
	if token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}
