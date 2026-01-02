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
	mqttUser := strings.TrimSpace(os.Getenv("MQTT_USERNAME"))
	mqttPass := strings.TrimSpace(os.Getenv("MQTT_PASSWORD"))

	log.Printf("starting alertmanager-webhook-mqtt-bridge")
	log.Printf("configuration: broker=%s, topic=%s, client_id=%s, listen_addr=%s", broker, topic, clientID, listenAddr)
	if mqttUser != "" {
		log.Printf("mqtt authentication enabled for user: %s", mqttUser)
	}

	client := connectMQTT(broker, clientID, mqttUser, mqttPass)
	log.Printf("mqtt client connected successfully to %s", broker)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		connected := client.IsConnected()
		status := "healthy"
		statusCode := http.StatusOK
		
		if !connected {
			status = "unhealthy"
			statusCode = http.StatusServiceUnavailable
			log.Printf("health check: mqtt client not connected")
		}
		
		response := map[string]interface{}{
			"status":        status,
			"mqtt_connected": connected,
			"broker":        broker,
			"topic":         topic,
		}
		
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	})

	http.HandleFunc("/alert", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("received alert webhook from %s", r.RemoteAddr)
		
		if r.Method != http.MethodPost {
			log.Printf("method not allowed: %s (expected POST)", r.Method)
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
			log.Printf("unsupported content type: %s", ct)
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}

		var payload webhookPayload
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			log.Printf("failed to decode json payload: %v", err)
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}

		log.Printf("processing webhook: %d alerts received", len(payload.Alerts))
		state, active := highestSeverity(payload.Alerts)
		log.Printf("calculated state: %s (%d active alerts)", state, active)
		
		if err := publishState(client, topic, state, active); err != nil {
			log.Printf("mqtt publish failed: %v", err)
			http.Error(w, "failed to publish", http.StatusBadGateway)
			return
		}

		log.Printf("successfully published state %s to topic %s", state, topic)
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("http server listening on %s", listenAddr)
	log.Printf("endpoints: POST /alert, GET /health")
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

func connectMQTT(broker, clientID, username, password string) mqtt.Client {
	log.Printf("connecting to mqtt broker: %s (client_id: %s)", broker, clientID)
	
	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID(clientID)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(2 * time.Second)
	
	// Add connection event handlers for logging
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Printf("mqtt client connected (reconnect)")
	})
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("mqtt connection lost: %v", err)
	})
	
	if username != "" {
		opts.SetUsername(username)
		opts.SetPassword(password)
		log.Printf("mqtt authentication configured")
	}

	client := mqtt.NewClient(opts)
	log.Printf("attempting mqtt connection...")
	token := client.Connect()
	if token.Wait() && token.Error() != nil {
		log.Fatalf("mqtt connect failed: %v", token.Error())
	}
	log.Printf("mqtt connection established successfully")
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
	message := mqttMessage{
		State:        state,
		ActiveAlerts: active,
		Source:       "alertmanager",
	}
	payload, err := json.Marshal(message)
	if err != nil {
		log.Printf("failed to marshal mqtt message: %v", err)
		return err
	}

	log.Printf("publishing to topic %s: state=%s, active_alerts=%d", topic, state, active)
	token := client.Publish(topic, 1, true, payload)
	if token.Wait() && token.Error() != nil {
		log.Printf("mqtt publish error: %v", token.Error())
		return token.Error()
	}
	log.Printf("mqtt message published successfully (qos=1, retained=true)")
	return nil
}
