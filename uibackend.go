package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	configMutex sync.RWMutex
	uiPort      = "8080"
)

// SafeConfig represents config without sensitive data
type SafeConfig struct {
	Prefix              string `json:"prefix"`
	AutoResponseEnabled bool   `json:"auto_response_enabled"`
	AutoResponsePhrase  string `json:"auto_response_phrase"`
	AutoEmojiEnabled    bool   `json:"auto_emoji_enabled"`
	AutoEmoji           string `json:"auto_emoji"`
	CurrentStatus       string `json:"current_status"`
	AutoPressureActive  bool   `json:"auto_pressure_active"`
}

// StatsResponse represents bot statistics
type StatsResponse struct {
	UptimeDays      int     `json:"uptime_days"`
	UptimeHours     int     `json:"uptime_hours"`
	UptimeMinutes   int     `json:"uptime_minutes"`
	CommandsHandled int     `json:"commands_handled"`
	MessagesLogged  int     `json:"messages_logged"`
	MemoryUsageMB   float64 `json:"memory_usage_mb"`
}

// ConfigUpdateRequest represents a config update request
type ConfigUpdateRequest struct {
	Prefix              *string `json:"prefix,omitempty"`
	AutoResponseEnabled *bool   `json:"auto_response_enabled,omitempty"`
	AutoResponsePhrase  *string `json:"auto_response_phrase,omitempty"`
	AutoEmojiEnabled    *bool   `json:"auto_emoji_enabled,omitempty"`
	AutoEmoji           *string `json:"auto_emoji,omitempty"`
}

// StatusUpdateRequest represents a status update request
type StatusUpdateRequest struct {
	Status     string `json:"status"`
	CustomText string `json:"custom_text,omitempty"`
}

func GetSafeConfig() SafeConfig {
	configMutex.RLock()
	autoResponderMutex.Lock()
	arEnabled := autoResponderEnabled
	autoResponderMutex.Unlock()
	
	apMutex.Lock()
	apActiveState := apActive
	apMutex.Unlock()
	
	//not fetching token !
	cfg := SafeConfig{
		Prefix:              config.Prefix,
		AutoResponseEnabled: arEnabled, // Use runtime state
		AutoResponsePhrase:  config.AutoResponsePhrase,
		AutoEmojiEnabled:    config.AutoReactEmojiEnabled,
		AutoEmoji:           config.AutoReactEmoji,
		CurrentStatus:       currentStatus,
		AutoPressureActive:  apActiveState,
	}
	configMutex.RUnlock()

	return cfg
}

func UpdateConfig(updates ConfigUpdateRequest) error {
	configMutex.Lock()
	defer configMutex.Unlock()

	if updates.Prefix != nil {
		config.Prefix = *updates.Prefix
	}
	if updates.AutoResponseEnabled != nil {
		config.AutoResponseEnabled = *updates.AutoResponseEnabled
		// sync runtime variable
		autoResponderMutex.Lock()
		autoResponderEnabled = config.AutoResponseEnabled
		autoResponderMutex.Unlock()
	}
	if updates.AutoResponsePhrase != nil {
		config.AutoResponsePhrase = *updates.AutoResponsePhrase
	}
	if updates.AutoEmojiEnabled != nil {
		config.AutoReactEmojiEnabled = *updates.AutoEmojiEnabled
	}
	if updates.AutoEmoji != nil {
		config.AutoReactEmoji = *updates.AutoEmoji
	}

	return saveConfig()
}

func GetStats() StatsResponse {
	statsMutex.Lock()
	uptime := time.Since(startTime)
	cmdHandled := commandsHandled
	msgLogged := messagesLogged
	statsMutex.Unlock()

	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memoryMB := float64(m.Alloc) / 1024 / 1024

	return StatsResponse{
		UptimeDays:      days,
		UptimeHours:     hours,
		UptimeMinutes:   minutes,
		CommandsHandled: cmdHandled,
		MessagesLogged:  msgLogged,
		MemoryUsageMB:   memoryMB,
	}
}

func ToggleAutoResponder() bool {
	autoResponderMutex.Lock()
	defer autoResponderMutex.Unlock()

	configMutex.Lock()
	config.AutoResponseEnabled = !config.AutoResponseEnabled
	autoResponderEnabled = config.AutoResponseEnabled
	configMutex.Unlock()

	saveConfig()
	return autoResponderEnabled
}

func ToggleAutoEmoji() bool {
	configMutex.Lock()
	defer configMutex.Unlock()

	config.AutoReactEmojiEnabled = !config.AutoReactEmojiEnabled
	if !config.AutoReactEmojiEnabled {
		config.AutoReactEmoji = ""
	}
	saveConfig()
	return config.AutoReactEmojiEnabled
}

func UpdateDiscordStatus(status string, customText string) error {
	statusText := ""
	switch status {
	case "online":
		statusText = StatusOnline
	case "idle":
		statusText = StatusIdle
	case "dnd", "do_not_disturb":
		statusText = StatusDND
	case "invisible", "offline":
		statusText = StatusInvisible
	default:
		return fmt.Errorf("invalid status: %s", status)
	}

	if err := updateStatusREST(statusText, customText); err != nil {
		return err
	}

	currentStatus = statusText
	return nil
}

func StopAutoPressure() bool {
	apMutex.Lock()
	defer apMutex.Unlock()

	if apActive {
		apActive = false
		if apStopChan != nil {
			close(apStopChan)
			apStopChan = nil
		}
		return true
	}
	return false
}

func IsAutoPressureActive() bool {
	apMutex.Lock()
	defer apMutex.Unlock()
	return apActive
}
func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func handleOptions(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.WriteHeader(http.StatusOK)
}

func apiGetConfig(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	safeConfig := GetSafeConfig()
	json.NewEncoder(w).Encode(safeConfig)
}

func apiUpdateConfig(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	var updates ConfigUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := UpdateConfig(updates); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update config: %v", err), http.StatusInternalServerError)
		return
	}

	safeConfig := GetSafeConfig()
	json.NewEncoder(w).Encode(safeConfig)
}

func apiGetStats(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	stats := GetStats()
	json.NewEncoder(w).Encode(stats)
}
func apiToggleAutoResponder(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	enabled := ToggleAutoResponder()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled": enabled,
		"message": fmt.Sprintf("Auto responder %s", map[bool]string{true: "enabled", false: "disabled"}[enabled]),
	})
}

func apiToggleAutoEmoji(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	enabled := ToggleAutoEmoji()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled": enabled,
		"message": fmt.Sprintf("Auto emoji %s", map[bool]string{true: "enabled", false: "disabled"}[enabled]),
	})
}

func apiUpdateStatus(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	var req StatusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := UpdateDiscordStatus(req.Status, req.CustomText); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update status: %v", err), http.StatusBadRequest)
		return
	}

	message := fmt.Sprintf("Status updated to %s", req.Status)
	if req.CustomText != "" {
		message = fmt.Sprintf("Status updated to %s with text: %s", req.Status, req.CustomText)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      req.Status,
		"custom_text": req.CustomText,
		"message":     message,
	})
}

func apiStopAutoPressure(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	w.Header().Set("Content-Type", "application/json")

	stopped := StopAutoPressure()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stopped": stopped,
		"message": map[bool]string{
			true:  "Auto pressure stopped",
			false: "Auto pressure was not active",
		}[stopped],
	})
}

func StartUIServer(port string) {
	if port == "" {
		port = uiPort
	}

	// API routes
	http.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleOptions(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			apiGetConfig(w, r)
		case http.MethodPut:
			apiUpdateConfig(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleOptions(w, r)
			return
		}
		if r.Method == http.MethodGet {
			apiGetStats(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/api/toggle/autoresponder", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleOptions(w, r)
			return
		}
		if r.Method == http.MethodPost {
			apiToggleAutoResponder(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/api/toggle/autoemoji", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleOptions(w, r)
			return
		}
		if r.Method == http.MethodPost {
			apiToggleAutoEmoji(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleOptions(w, r)
			return
		}
		if r.Method == http.MethodPost {
			apiUpdateStatus(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/api/autopressure/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			handleOptions(w, r)
			return
		}
		if r.Method == http.MethodPost {
			apiStopAutoPressure(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	uiDir := "./ui"
	if _, err := os.Stat(uiDir); os.IsNotExist(err) {
		os.MkdirAll(uiDir, 0755)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			indexPath := filepath.Join(uiDir, "index.html")
			http.ServeFile(w, r, indexPath)
			return
		}
		http.FileServer(http.Dir(uiDir)).ServeHTTP(w, r)
	})

	addr := fmt.Sprintf("localhost:%s", port)
	log.Printf("Web UI server starting on http://%s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Printf("Failed to start UI server: %v", err)
	}
}
