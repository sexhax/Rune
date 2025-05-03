package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	
	"github.com/gorilla/websocket"
	discordrpc "selfbot/rpc"
)

// Config represents the configuration for the selfbot
type Config struct {
	Token   string  `json:"token"`
	OwnerID float64 `json:"owner_id"`
	Prefix  string  `json:"prefix"`
	RPC     struct {
		Enabled    bool   `json:"enabled"`
		ApplicationID string `json:"application_id"`
		State      string `json:"state"`
		Details    string `json:"details"`
		LargeImage string `json:"large_image"`
		LargeText  string `json:"large_text"`
	} `json:"rpc"`
}

// Message represents a Discord message
type Message struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Author    struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
		Avatar   string `json:"avatar"`
	} `json:"author"`
	Mentions []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Avatar   string `json:"avatar"`
	} `json:"mentions"`
}

// Websocket payload structure
type WSPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  int             `json:"s"`
	T  string          `json:"t"`
}

// Gateway events
const (
	GatewayOpcodeDispatch           = 0
	GatewayOpcodeHeartbeat          = 1
	GatewayOpcodeIdentify           = 2
	GatewayOpcodeStatusUpdate       = 3
	GatewayOpcodeVoiceStateUpdate   = 4
	GatewayOpcodeVoiceServerPing    = 5
	GatewayOpcodeResume             = 6
	GatewayOpcodeReconnect          = 7
	GatewayOpcodeRequestGuildMembers = 8
	GatewayOpcodeInvalidSession     = 9
	GatewayOpcodeHello              = 10
	GatewayOpcodeHeartbeatACK       = 11
)

// Status types
const (
	StatusOnline    = "online"
	StatusIdle      = "idle"
	StatusDND       = "dnd"
	StatusInvisible = "invisible"
)

// Variables
var (
	config          Config
	wsConn          *websocket.Conn
	heartbeatTicker *time.Ticker
	sequence        int
	sessionID       string
	lastHeartbeat   time.Time
	lastMessageID   string
	startTime       = time.Now()
	
	// Stats
	messagesLogged  int
	commandsHandled int
	statsMutex      sync.Mutex
	
	// Auto responder
	autoResponderEnabled bool
	autoResponderMutex   sync.Mutex
	
	// Auto pressure targets
	autoPressureTargets = make(map[string]chan bool)
	apMutex             sync.Mutex
	apActive            bool
	apStopChan          chan bool
	apTargetID          string
	apWords             = []string{"je kanker vader", "Ratio", "Clown", "Bozo", "Failure", "Trash", "Cry", "Skill issue", "nigger", "ape", "monkey boy", "ching chong"}
	
	// Current status
	currentStatus   = StatusOnline
	
	// For urban dictionary
	urbanCache = make(map[string][]UrbanDefinition)
	
	// RPC client
	rpcClient *discordrpc.Client
)

// Initialize config and other startup tasks
func init() {
	// Read config file
	configFile, err := os.ReadFile("config.json")
	if err != nil {
		fmt.Println("Error reading config file:", err)
		// Create a default config if file doesn't exist
		if os.IsNotExist(err) {
			config = Config{
				Token:   "YOUR_TOKEN_HERE",
				OwnerID: 0,
				Prefix:  "&",
				RPC: struct {
					Enabled       bool   `json:"enabled"`
					ApplicationID string `json:"application_id"`
					State         string `json:"state"`
					Details       string `json:"details"`
					LargeImage    string `json:"large_image"`
					LargeText     string `json:"large_text"`
				}{
					Enabled:       false,
					ApplicationID: "",
					State:         "Running RUNE",
					Details:       "A Discord selfbot",
					LargeImage:    "logo",
					LargeText:     "RUNE Selfbot",
				},
			}
			saveConfig()
			fmt.Println("Created default config file. Please edit config.json with your token and restart.")
			os.Exit(0)
		}
		os.Exit(1)
	}
	
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		fmt.Println("Error parsing config:", err)
		os.Exit(1)
	}
	
	// Validate config
	if config.Token == "" || config.Token == "YOUR_TOKEN_HERE" {
		fmt.Println("Please set your Discord token in config.json")
		os.Exit(1)
	}
	
	// Seed the RNG
	rand.Seed(time.Now().UnixNano())
}

// Weather data structures
type IPGeolocation struct {
	IP       string  `json:"ip"`
	City     string  `json:"city"`
	Region   string  `json:"region"`
	Country  string  `json:"country"`
	Loc      string  `json:"loc"`
	Timezone string  `json:"timezone"`
	Latitude  float64 `json:"-"`
	Longitude float64 `json:"-"`
}

type WeatherData struct {
	Main struct {
		Temp      float64 `json:"temp"`
		FeelsLike float64 `json:"feels_like"`
		Humidity  int     `json:"humidity"`
		Pressure  int     `json:"pressure"`
	} `json:"main"`
	Weather []struct {
		Main        string `json:"main"`
		Description string `json:"description"`
	} `json:"weather"`
	Wind struct {
		Speed float64 `json:"speed"`
	} `json:"wind"`
	Name string `json:"name"`
}

// Connect to Discord's gateway
func connectWebsocket() error {
	// Get gateway URL
	gatewayURL, err := getGatewayURL()
	if err != nil {
		return fmt.Errorf("failed to get gateway URL: %w", err)
	}
	
	// Connect to gateway
	conn, _, err := websocket.DefaultDialer.Dial(gatewayURL+"/?v=10&encoding=json", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}
	
	wsConn = conn
	
	// Handle hello message
	var payload WSPayload
	if err := wsConn.ReadJSON(&payload); err != nil {
		return fmt.Errorf("failed to read hello: %w", err)
	}
	
	if payload.Op != GatewayOpcodeHello {
		return fmt.Errorf("expected hello op code, got %d", payload.Op)
	}
	
	// Parse heartbeat interval
	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(payload.D, &helloData); err != nil {
		return fmt.Errorf("failed to parse hello data: %w", err)
	}
	
	// Start heartbeat
	go startHeartbeat(helloData.HeartbeatInterval)
	
	// Identify with the gateway
	identify := map[string]interface{}{
		"op": GatewayOpcodeIdentify,
		"d": map[string]interface{}{
			"token": config.Token,
			"properties": map[string]string{
				"os":      runtime.GOOS,
				"browser": "Chrome",
				"device":  "selfbot",
			},
			"compress": false,
			"presence": map[string]interface{}{
				"since":      nil,
				"activities": []interface{}{},
				"status":     "online",
				"afk":        false,
			},
		},
	}
	
	if err := wsConn.WriteJSON(identify); err != nil {
		return fmt.Errorf("failed to identify: %w", err)
	}
	
	return nil
}

// Get the gateway URL from Discord's API
func getGatewayURL() (string, error) {
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/gateway", nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("Authorization", config.Token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("bad status code: %d", resp.StatusCode)
	}
	
	var data struct {
		URL string `json:"url"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	
	return data.URL, nil
}

// Send heartbeats periodically
func startHeartbeat(interval int) {
	heartbeatTicker = time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer heartbeatTicker.Stop()
	
	for range heartbeatTicker.C {
		heartbeat := map[string]interface{}{
			"op": GatewayOpcodeHeartbeat,
			"d":  sequence,
		}
		
		if err := wsConn.WriteJSON(heartbeat); err != nil {
			fmt.Printf("Error sending heartbeat: %v\n", err)
			// Try to reconnect on heartbeat failure
			if err := connectWebsocket(); err != nil {
				fmt.Printf("Failed to reconnect: %v\n", err)
			}
			return
		}
	}
}

// Main message handling function
func listenForMessages() {
	for {
		var payload WSPayload
		if err := wsConn.ReadJSON(&payload); err != nil {
			fmt.Printf("Error reading from websocket: %v\n", err)
			
			// Try to reconnect
			if err := connectWebsocket(); err != nil {
				fmt.Printf("Failed to reconnect: %v\n", err)
				time.Sleep(5 * time.Second)
			}
			continue
		}
		
		// Update sequence number if provided
		if payload.S != 0 {
			sequence = payload.S
		}
		
		// Handle different opcodes
		switch payload.Op {
		case GatewayOpcodeDispatch:
			fmt.Printf("Received message: op=%d, t=%s\n", payload.Op, payload.T)
			
			switch payload.T {
			case "READY":
				var readyData struct {
					SessionID string `json:"session_id"`
					User      struct {
						ID       string `json:"id"`
						Username string `json:"username"`
					} `json:"user"`
				}
				
				if err := json.Unmarshal(payload.D, &readyData); err != nil {
					fmt.Printf("Error parsing READY data: %v\n", err)
					continue
				}
				
				sessionID = readyData.SessionID
				fmt.Printf("Connected as %s\n", readyData.User.Username)
				
			case "MESSAGE_CREATE":
				var message Message
				if err := json.Unmarshal(payload.D, &message); err != nil {
					fmt.Printf("Error parsing MESSAGE_CREATE data: %v\n", err)
					continue
				}
				
				// Only process new messages
				messageTime, err := time.Parse(time.RFC3339, message.Timestamp)
				if err != nil || !messageTime.After(startTime) {
					continue
				}
				
				// Log the message (non-bot messages)
				if !message.Author.Bot {
					fmt.Printf("Message from %s: %s\n", message.Author.Username, message.Content)
					
					statsMutex.Lock()
					messagesLogged++
					statsMutex.Unlock()
					
					// Check for autoresponder - if message mentions us and we're not the author
					ownerIDStr := fmt.Sprintf("%.0f", config.OwnerID)
					if message.Author.ID != ownerIDStr && autoResponderEnabled {
						// Check if the message mentions the selfbot user
						selfMentioned := false
						for _, mention := range message.Mentions {
							if mention.ID == ownerIDStr {
								selfMentioned = true
								break
							}
						}
						
						// Check if the message text contains a direct mention of the owner ID
						if !selfMentioned && strings.Contains(message.Content, "<@"+ownerIDStr+">") {
							selfMentioned = true
						}
						
						if selfMentioned {
							fmt.Printf("Autoresponder triggered by %s\n", message.Author.Username)
							// Reply with the auto-response message
							autoResponse := "```[RUNE]\n\nHey, " + message.Author.Username + "!\n\nI am currently not behind my pc or in the mood to respond, please try dming me or message me at a later time instead.```"
							sendMessage(message.ChannelID, autoResponse)
						}
					}
				}
				
				// Check if message is a command from owner
				ownerIDStr := fmt.Sprintf("%.0f", config.OwnerID)
				fmt.Printf("Message author ID: %s, Owner ID: %s\n", message.Author.ID, ownerIDStr)
				
				// Convert the owner ID to an integer for comparison if needed
				if message.Author.ID == ownerIDStr || message.Author.Username == "fedgooner" {
					fmt.Printf("Owner command detected: %s\n", message.Content)
					if strings.HasPrefix(message.Content, config.Prefix) {
						handleMessage(message)
					}
				} else {
					fmt.Printf("Message not from owner! Author: %s (ID: %s), Owner: %s\n", 
						message.Author.Username, message.Author.ID, ownerIDStr)
				}
			}
			
		case GatewayOpcodeHeartbeatACK:
			// Heartbeat acknowledged, all good
			
		case GatewayOpcodeReconnect:
			fmt.Println("Server requested reconnect")
			if err := connectWebsocket(); err != nil {
				fmt.Printf("Failed to reconnect: %v\n", err)
			}
			
		case GatewayOpcodeInvalidSession:
			fmt.Println("Invalid session, reconnecting...")
			time.Sleep(5 * time.Second)
			if err := connectWebsocket(); err != nil {
				fmt.Printf("Failed to reconnect: %v\n", err)
			}
		}
	}
}

// API functions
func sendMessage(channelID, content string) string {
	fmt.Printf("Attempting to send message to channel %s: %s\n", channelID, content)
	
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	fmt.Printf("POST URL: %s\n", url)
	
	// Create request body
	reqBody := map[string]string{
		"content": content,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return ""
	}
	
	fmt.Printf("Request body: %s\n", string(jsonBody))
	
	// Create request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		fmt.Println("Error creating request:", err)
		return ""
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", config.Token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	// Send request
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error sending message:", err)
		return ""
	}
	defer resp.Body.Close()
	
	// Check response
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error sending message: %s (status code: %d)\nResponse: %s\n", 
			resp.Status, resp.StatusCode, string(body))
		return ""
	} else {
		fmt.Printf("Message sent successfully to channel %s\n", channelID)
		
		// Parse response to get message ID
		var msgResponse struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&msgResponse); err != nil {
			fmt.Println("Error parsing message response:", err)
			return ""
		}
		return msgResponse.ID
	}
}

// Edit a message that was previously sent
func editMessage(channelID, messageID, newContent string) bool {
	if messageID == "" {
		fmt.Println("Cannot edit message: messageID is empty")
		return false
	}
	
	fmt.Printf("Attempting to edit message %s in channel %s\n", messageID, channelID)
	
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages/%s", channelID, messageID)
	
	// Create request body
	reqBody := map[string]string{
		"content": newContent,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Println("Error marshaling JSON for edit:", err)
		return false
	}
	
	// Create request
	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		fmt.Println("Error creating edit request:", err)
		return false
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", config.Token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	// Send request
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error editing message:", err)
		return false
	}
	defer resp.Body.Close()
	
	// Check response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error editing message: %s (status code: %d)\nResponse: %s\n", 
			resp.Status, resp.StatusCode, string(body))
		return false
	}
	
	fmt.Printf("Message %s edited successfully\n", messageID)
	return true
}

func deleteMessage(channelID, messageID string) bool {
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages/%s", channelID, messageID)
	
	// Create request
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		fmt.Println("Error creating delete request:", err)
		return false
	}
	
	// Set headers
	req.Header.Set("Authorization", config.Token)
	
	// Send request
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error deleting message:", err)
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == http.StatusNoContent
}

func deleteMessages(channelID string, count int) int {
	// Get message history
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%d", channelID, count)
	
	// Create request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println("Error creating request for message history:", err)
		return 0
	}
	
	// Set headers
	req.Header.Set("Authorization", config.Token)
	
	// Send request
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error getting message history:", err)
		return 0
	}
	
	// Parse response
	var messages []struct {
		ID string `json:"id"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		fmt.Println("Error parsing message history:", err)
		resp.Body.Close()
		return 0
	}
	resp.Body.Close()
	
	// Delete messages
	deletedCount := 0
	for _, msg := range messages {
		// Only delete messages that we can (our own messages)
		if deleteMessage(channelID, msg.ID) {
			deletedCount++
			
			// Discord rate limits, so add a small delay
			time.Sleep(time.Millisecond * 300)
		}
	}
	
	return deletedCount
}

// Handle a message
func handleMessage(message Message) {
	// Parse command and arguments
	fmt.Printf("Starting command handling for message: %s\n", message.Content)
	content := strings.TrimPrefix(message.Content, config.Prefix)
	args := strings.Split(content, " ")
	
	if len(args) == 0 || args[0] == "" {
		fmt.Println("Command was empty after parsing")
		return
	}
	
	command := args[0]
	
	if len(args) > 1 {
		args = args[1:]
	} else {
		args = []string{}
	}
	
	// Process commands
	fmt.Printf("Processing command: %s with args: %v\n", command, args)
	
	statsMutex.Lock()
	commandsHandled++ // Increment command counter
	statsMutex.Unlock()
	
	// Process and respond to the command
	switch command {
	case "help":
		fmt.Println("Executing help command...")
		handleHelp(message)
	case "categories":
		fmt.Println("Executing categories command...")
		handleCategories(message)
	case "utilities":
		fmt.Println("Executing utilities command...")
		handleUtilities(message)
	case "fun":
		fmt.Println("Executing fun command...")
		handleFun(message)
	case "info":
		fmt.Println("Executing info command...")
		handleInfo(message)
	case "ping":
		fmt.Println("Executing ping command...")
		handlePing(message)
	case "say":
		fmt.Println("Executing say command...")
		handleSay(message, args)
		// Note: handleSay already deletes the message, so we'll return early
		return
	case "clear":
		fmt.Println("Executing clear command...")
		handleClear(message, args)
	case "avatar":
		fmt.Println("Executing avatar command...")
		handleAvatar(message)
	case "whoami":
		fmt.Println("Executing whoami command...")
		handleUserInfo(message)
	case "femboy":
		fmt.Println("Executing femboy command...")
		handleFemboy(message, args)
	case "8ball":
		fmt.Println("Executing 8ball command...")
		handle8Ball(message, args)
	case "roll":
		fmt.Println("Executing roll command...")
		handleRoll(message, args)
	case "rizz":
		fmt.Println("Executing rizz command...")
		handleRizz(message, args)
	case "ar":
		fmt.Println("Executing ar command...")
		handleAutoResponder(message)
	case "weather":
		fmt.Println("Executing weather command...")
		handleWeather(message)
	case "quote":
		fmt.Println("Executing quote command...")
		handleQuote(message)
	case "stats":
		fmt.Println("Executing stats command...")
		handleStats(message)
	case "credits":
		fmt.Println("Executing credits command...")
		handleCredits(message)
	case "ap":
		fmt.Println("Executing ap command...")
		handleAutoPressure(message, args)
	case "status":
		fmt.Println("Executing status command...")
		handleStatus(message, args)
	case "joke":
		fmt.Println("Executing joke command...")
		handleJoke(message)
	case "urban":
		fmt.Println("Executing urban command...")
		handleUrban(message, args)
	case "coinflip":
		fmt.Println("Executing coinflip command...")
		handleCoinFlip(message)
	case "fact":
		fmt.Println("Executing fact command...")
		handleFact(message)
	case "encode":
		fmt.Println("Executing encode command...")
		handleEncode(message, args)
	case "decode":
		fmt.Println("Executing decode command...")
		handleDecode(message, args)
	case "password":
		fmt.Println("Executing password command...")
		handlePassword(message, args)
	case "ip":
		fmt.Println("Executing ip command...")
		handleIPLookup(message, args)
	case "meme":
		fmt.Println("Executing meme command...")
		handleMemePhrase(message)
	case "shorten":
		fmt.Println("Executing shorten command...")
		handleShortenURL(message, args)
	case "rpc":
		fmt.Println("Executing rpc command...")
		handleRPC(message, args)
	case "cat":
		fmt.Println("Executing cat command...")
		handleCat(message)
	case "psearch":
		fmt.Println("Executing psearch command...")
		handlePornhubSearch(message, args)
	case "tits":
		fmt.Println("Executing tits command...")
		handleTits(message)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nUnknown command: `%s`. Type %shelp for a list of commands.```", command, config.Prefix))
	}
	
	// Delete the command message after processing
	if deleted := deleteMessage(message.ChannelID, message.ID); deleted {
		fmt.Printf("Deleted command message: %s\n", message.ID)
	} else {
		fmt.Printf("Failed to delete command message: %s\n", message.ID)
	}
	
	fmt.Printf("Command processing completed for: %s\n", command)
}

// Updated command handlers
func handleHelp(message Message) {
	helpText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"Commands:\n" +
		"\u001b[0;32m" + config.Prefix + "help\u001b[0m - Show this help message\n" +
		"\u001b[0;32m" + config.Prefix + "categories\u001b[0m - Show all command categories\n" +
		"\u001b[0;32m" + config.Prefix + "utilities\u001b[0m - Show utility commands\n" +
		"\u001b[0;32m" + config.Prefix + "fun\u001b[0m - Show fun commands\n" +
		"\u001b[0;32m" + config.Prefix + "info\u001b[0m - Show information commands\n\n" +
		"Tip: Type " + config.Prefix + "help <command> for detailed help on a specific command\n" +
		"```"
	sendMessage(message.ChannelID, helpText)
}

// New command - Categories
func handleCategories(message Message) {
	categoriesText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"Command Categories:\n\n" +
		"\u001b[0;33mUtilities\u001b[0m - Useful tools and functions\n" +
		"\u001b[0;33mFun\u001b[0m - Entertainment and random commands\n" +
		"\u001b[0;33mInfo\u001b[0m - Information and statistics\n\n" +
		"Use " + config.Prefix + "<category> to see commands in each category\n" +
		"```"
	sendMessage(message.ChannelID, categoriesText)
}

// New command - Utilities category
func handleUtilities(message Message) {
	utilitiesText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"\u001b[0;33mUtility Commands:\u001b[0m\n\n" +
		"\u001b[0;32m" + config.Prefix + "ping\u001b[0m - Check bot latency\n" +
		"\u001b[0;32m" + config.Prefix + "clear [count]\u001b[0m - Delete messages (default: 10)\n" +
		"\u001b[0;32m" + config.Prefix + "say <message>\u001b[0m - Make the bot say something\n" +
		"\u001b[0;32m" + config.Prefix + "weather [location]\u001b[0m - Get current weather\n" +
		"\u001b[0;32m" + config.Prefix + "ar\u001b[0m - Toggle auto responder\n" +
		"\u001b[0;32m" + config.Prefix + "ap @user\u001b[0m - Start autopressure on user\n" +
		"\u001b[0;32m" + config.Prefix + "ap stop\u001b[0m - Stop autopressure\n" +
		"\u001b[0;32m" + config.Prefix + "status <online|idle|dnd|invisible>\u001b[0m - Change Discord status\n" +
		"\u001b[0;32m" + config.Prefix + "rpc <on|off|status|set>\u001b[0m - Manage Discord Rich Presence\n" +
		"\u001b[0;32m" + config.Prefix + "ip <address>\u001b[0m - Lookup IP information\n" +
		"\u001b[0;32m" + config.Prefix + "encode <text>\u001b[0m - Encode text to base64\n" +
		"\u001b[0;32m" + config.Prefix + "decode <text>\u001b[0m - Decode base64 to text\n" +
		"\u001b[0;32m" + config.Prefix + "password [length]\u001b[0m - Generate a secure password\n" +
		"\u001b[0;32m" + config.Prefix + "shorten <url>\u001b[0m - Shorten a URL\n" +
		"```"
	sendMessage(message.ChannelID, utilitiesText)
}

// New command - Fun category
func handleFun(message Message) {
	funText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"\u001b[0;33mFun Commands:\u001b[0m\n\n" +
		"\u001b[0;32m" + config.Prefix + "8ball <question>\u001b[0m - Ask the magic 8ball\n" +
		"\u001b[0;32m" + config.Prefix + "roll [sides]\u001b[0m - Roll a die (default: 6 sides)\n" +
		"\u001b[0;32m" + config.Prefix + "rizz\u001b[0m - Get a random pickup line\n" +
		"\u001b[0;32m" + config.Prefix + "femboy\u001b[0m - Calculate femboy percentage\n" +
		"\u001b[0;32m" + config.Prefix + "quote\u001b[0m - Get a random quote\n" +
		"\u001b[0;32m" + config.Prefix + "joke\u001b[0m - Get a random joke\n" +
		"\u001b[0;32m" + config.Prefix + "urban <term>\u001b[0m - Look up a term on Urban Dictionary\n" +
		"\u001b[0;32m" + config.Prefix + "coinflip\u001b[0m - Flip a coin\n" +
		"\u001b[0;32m" + config.Prefix + "fact\u001b[0m - Get a random fact\n" +
		"\u001b[0;32m" + config.Prefix + "cat\u001b[0m - Get a random cat picture\n" +
		"\u001b[0;32m" + config.Prefix + "psearch <term>\u001b[0m - Search PornHub for videos\n" +
		"\u001b[0;32m" + config.Prefix + "tits\u001b[0m - Get a random tits image\n" +
		"\u001b[0;32m" + config.Prefix + "meme\u001b[0m - Get a random meme\n" +
		"```"
	sendMessage(message.ChannelID, funText)
}

// New command - Info category
func handleInfo(message Message) {
	infoText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"\u001b[0;33mInfo Commands:\u001b[0m\n\n" +
		"\u001b[0;32m" + config.Prefix + "whoami\u001b[0m - Show your user info\n" +
		"\u001b[0;32m" + config.Prefix + "avatar\u001b[0m - Get your avatar URL\n" +
		"\u001b[0;32m" + config.Prefix + "stats\u001b[0m - Show bot statistics\n" +
		"\u001b[0;32m" + config.Prefix + "credits\u001b[0m - Display bot credits\n" +
		"```"
	sendMessage(message.ChannelID, infoText)
}

// Updated command handlers
func handlePing(message Message) {
	// Add ping calculation
	start := time.Now()
	
	// Make a simple API request to measure latency
	req, _ := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	req.Header.Set("Authorization", config.Token)
	client := &http.Client{}
	resp, err := client.Do(req)
	
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nError calculating ping: connection failed```")
		return
	}
	defer resp.Body.Close()
	
	latency := time.Since(start).Milliseconds()
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🏓 Pong! Latency: %dms```", latency))
}

// New command - Auto Responder
func handleAutoResponder(message Message) {
	autoResponderMutex.Lock()
	autoResponderEnabled = !autoResponderEnabled
	status := "enabled"
	if !autoResponderEnabled {
		status = "disabled"
	}
	autoResponderMutex.Unlock()
	
	fmt.Printf("Auto responder %s\n", status)
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nAuto responder %s```", status))
}

// New command - Femboy percentage
func handleFemboy(message Message, args []string) {
	var targetUsername string
	
	// Check if there's a mention in the message
	if len(message.Mentions) > 0 {
		// Use the first mentioned user
		targetUsername = message.Mentions[0].Username
	} else if len(args) > 0 {
		// Try to extract mentions from the text if any
		mentions := extractMentions(message.Content)
		if len(mentions) > 0 {
			// Use "user" as fallback since we don't have a way to get username by ID
			targetUsername = "user"
		} else {
			// No mentions found, use the argument as the username
			targetUsername = strings.Join(args, " ")
		}
	} else {
		// No arguments or mentions, default to "You"
		targetUsername = "You"
	}
	
	// Generate a random percentage
	rand.Seed(time.Now().UnixNano())
	percentage := rand.Intn(101)
	
	// Build the response with ANSI formatting
	response := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n%s, you are %d%% femboy :3```", targetUsername, percentage)
	
	sendMessage(message.ChannelID, response)
}

// New command - 8ball
func handle8Ball(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease ask a question!```")
		return
	}
	
	// 8ball responses
	responses := []string{
		"It is certain.",
		"It is decidedly so.",
		"Without a doubt.",
		"Yes, definitely.",
		"You may rely on it.",
		"As I see it, yes.",
		"Most likely.",
		"Outlook good.",
		"Yes.",
		"Signs point to yes.",
		"Reply hazy, try again.",
		"Ask again later.",
		"Better not tell you now.",
		"Cannot predict now.",
		"Concentrate and ask again.",
		"Don't count on it.",
		"My reply is no.",
		"My sources say no.",
		"Outlook not so good.",
		"Very doubtful.",
	}
	
	rand.Seed(time.Now().UnixNano())
	response := responses[rand.Intn(len(responses))]
	
	// Get the question from args and ensure it has a question mark at the end
	question := strings.Join(args, " ")
	if !strings.HasSuffix(question, "?") {
		question += "?"
	}
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n%s\n\n🎱 %s```", question, response))
}

// New command - Roll dice
func handleRoll(message Message, args []string) {
	sides := 6 // Default 6-sided die
	
	if len(args) > 0 {
		if s, err := strconv.Atoi(args[0]); err == nil && s > 0 {
			sides = s
		}
	}
	
	rand.Seed(time.Now().UnixNano())
	result := rand.Intn(sides) + 1
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🎲 You rolled a %d (d%d)```", result, sides))
}

// Updated Rizz command - Using API for pickup lines
func handleRizz(message Message, args []string) {
	// Send a temporary message while we fetch the pickup line
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔄 Fetching rizz line...```")
	
	// Define the rizz API response structure
	type RizzResponse struct {
		ID       string `json:"_id"`
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	
	// Make requests to the API until we get an English response
	var line string
	maxRetries := 5 // Limit retries to avoid infinite loop
	
	for i := 0; i < maxRetries; i++ {
		resp, err := http.Get("https://rizzapi.vercel.app/random")
		if err != nil {
			continue // Try again on connection error
		}
		
		// Read the response
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue // Try again on non-200 status
		}
		
		var rizzResponse RizzResponse
		if err := json.NewDecoder(resp.Body).Decode(&rizzResponse); err != nil {
			resp.Body.Close()
			continue // Try again on parse error
		}
		resp.Body.Close()
		
		// Check if the language is English
		if rizzResponse.Language == "English" {
			line = rizzResponse.Text
			break
		}
	}
	
	// If we didn't get a successful response after all retries, use a fallback
	if line == "" {
		// Fallback pickup lines
		fallbackLines := []string{
			"Are you a magician? Because whenever I look at you, everyone else disappears.",
			"Do you have a map? I keep getting lost in your eyes.",
			"Is your name Google? Because you have everything I've been searching for.",
			"Are you a camera? Because every time I look at you, I smile.",
			"Do you have a Band-Aid? Because I just scraped my knee falling for you.",
			"If you were a vegetable, you'd be a cute-cumber.",
			"Are you made of copper and tellurium? Because you're Cu-Te.",
			"Are you a parking ticket? Because you've got FINE written all over you.",
			"Is your name WiFi? Because I'm feeling a connection.",
			"Are you a bank loan? Because you have my interest.",
		}
		
		rand.Seed(time.Now().UnixNano())
		line = fallbackLines[rand.Intn(len(fallbackLines))]
	}
	
	// Update the temporary message with the pickup line
	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❤️ %s```", line))
}

// Get user's location based on IP address
func getLocationFromIP() (*IPGeolocation, error) {
	// Using ipinfo.io to get location data
	resp, err := http.Get("https://ipinfo.io/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get IP info: %s", resp.Status)
	}
	
	// Parse the response
	var geolocation IPGeolocation
	if err := json.NewDecoder(resp.Body).Decode(&geolocation); err != nil {
		return nil, err
	}
	
	// Parse latitude and longitude
	if geolocation.Loc != "" {
		coords := strings.Split(geolocation.Loc, ",")
		if len(coords) == 2 {
			geolocation.Latitude, _ = strconv.ParseFloat(coords[0], 64)
			geolocation.Longitude, _ = strconv.ParseFloat(coords[1], 64)
		}
	}
	
	return &geolocation, nil
}

// Get weather data from OpenWeatherMap
func getWeatherData(lat, lon float64) (*WeatherData, error) {
	apiKey := "9de243494c0b295cca9337e1e96b00e2" // OpenWeatherMap API key
	url := fmt.Sprintf("https://api.openweathermap.org/data/2.5/weather?lat=%f&lon=%f&units=metric&appid=%s", 
		lat, lon, apiKey)
	
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("weather API returned status %d", resp.StatusCode)
	}
	
	var weatherData WeatherData
	err = json.NewDecoder(resp.Body).Decode(&weatherData)
	if err != nil {
		return nil, err
	}
	
	return &weatherData, nil
}

// Get weather emoji based on condition
func getWeatherEmoji(condition string) string {
	condition = strings.ToLower(condition)
	
	switch condition {
	case "clear":
		return "☀️"
	case "clouds":
		return "☁️"
	case "rain":
		return "🌧️"
	case "drizzle":
		return "🌦️"
	case "thunderstorm":
		return "⛈️"
	case "snow":
		return "❄️"
	case "mist", "fog", "haze":
		return "🌫️"
	case "dust", "sand":
		return "🌪️"
	default:
		return "☀️"
	}
}

// New command - Weather
func handleWeather(message Message) {
	// Extract location from the message
	args := strings.Fields(message.Content)[1:]
	location := ""
	
	statusMsg := "🔄 Fetching weather data"
	statusMsgID := sendMessage(message.ChannelID, statusMsg)
	
	if len(args) > 0 {
		location = strings.Join(args, " ")
		statusMsg = fmt.Sprintf("🔄 Fetching weather for %s", location)
		editMessage(message.ChannelID, statusMsgID, statusMsg)
	}
	
	var lat, lon float64
	var locationName string
	var err error
	
	if location != "" {
		// Get coordinates for provided location
		locationData, err := getLocationCoordinates(location)
		if err != nil {
			editMessage(message.ChannelID, statusMsgID, "❌ Error: "+err.Error())
			return
		}
		
		if len(locationData) == 0 {
			editMessage(message.ChannelID, statusMsgID, "❌ Location not found")
			return
		}
		
		lat = locationData[0].Lat
		lon = locationData[0].Lon
		locationName = locationData[0].Name
		if locationData[0].Country != "" {
			locationName += ", " + locationData[0].Country
		}
	} else {
		// Try to get user location from IP
		ipInfo, err := getUserLocationFromIP()
		if err != nil {
			// Fall back to random weather if IP geolocation fails
			weatherData := getRandomWeather()
			editMessage(message.ChannelID, statusMsgID, formatWeatherMessage(weatherData, "Random Location"))
			return
		}
		
		lat = ipInfo.Lat
		lon = ipInfo.Lon
		locationName = ipInfo.City
		if ipInfo.Country != "" {
			locationName += ", " + ipInfo.Country
		}
	}
	
	// Get weather data using coordinates
	weatherData, err := getWeatherData(lat, lon)
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "❌ Error fetching weather: "+err.Error())
		return
	}
	
	// Format and send weather message
	weatherMsg := formatWeatherMessage(weatherData, locationName)
	editMessage(message.ChannelID, statusMsgID, weatherMsg)
}

// GeocodingResponse represents the OpenWeatherMap geocoding API response
type GeocodingResponse []struct {
	Name       string  `json:"name"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Country    string  `json:"country"`
	State      string  `json:"state,omitempty"`
}

// IPInfo represents the data returned from IP geolocation
type IPInfo struct {
	IP        string  `json:"ip"`
	City      string  `json:"city"`
	Region    string  `json:"region"`
	Country   string  `json:"country"`
	Lat       float64 `json:"latitude"`
	Lon       float64 `json:"longitude"`
}

// Get location coordinates from OpenWeatherMap Geocoding API
func getLocationCoordinates(location string) (GeocodingResponse, error) {
	apiKey := "9de243494c0b295cca9337e1e96b00e2" // OpenWeatherMap API key
	apiURL := fmt.Sprintf("http://api.openweathermap.org/geo/1.0/direct?q=%s&limit=1&appid=%s", 
		url.QueryEscape(location), apiKey)
	
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("geocoding API returned status %d", resp.StatusCode)
	}
	
	var geocodingResp GeocodingResponse
	err = json.NewDecoder(resp.Body).Decode(&geocodingResp)
	if err != nil {
		return nil, err
	}
	
	return geocodingResp, nil
}

// Get user's approximate location from IP
func getUserLocationFromIP() (*IPInfo, error) {
	resp, err := http.Get("https://ipapi.co/json/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("IP API returned status %d", resp.StatusCode)
	}
	
	var ipInfo IPInfo
	err = json.NewDecoder(resp.Body).Decode(&ipInfo)
	if err != nil {
		return nil, err
	}
	
	return &ipInfo, nil
}

// Format weather data into a Discord message
func formatWeatherMessage(data *WeatherData, location string) string {
	condition := ""
	if len(data.Weather) > 0 {
		condition = data.Weather[0].Main
	}
	
	emoji := getWeatherEmoji(condition)
	
	return fmt.Sprintf("**Weather for %s** %s\n\n"+
		"🌡️ Temperature: **%.1f°C**\n"+
		"🤔 Feels like: **%.1f°C**\n"+
		"💧 Humidity: **%d%%**\n"+
		"💨 Wind: **%.1f m/s**\n"+
		"🔍 Condition: **%s**",
		location, emoji,
		data.Main.Temp, 
		data.Main.FeelsLike,
		data.Main.Humidity,
		data.Wind.Speed,
		strings.Title(strings.ToLower(condition)))
}

// Generate random weather data (fallback)
func getRandomWeather() *WeatherData {
	conditions := []string{"Clear", "Clouds", "Rain", "Thunderstorm", "Snow", "Mist"}
	randomCondition := conditions[rand.Intn(len(conditions))]
	
	weatherData := &WeatherData{}
	weatherData.Main.Temp = float64(rand.Intn(35)) - 5 // -5 to 30 degrees
	weatherData.Main.FeelsLike = weatherData.Main.Temp - 2 + rand.Float64()*4
	weatherData.Main.Humidity = rand.Intn(100)
	weatherData.Wind.Speed = rand.Float64() * 10
	weatherData.Weather = []struct {
		Main        string `json:"main"`
		Description string `json:"description"`
	}{
		{
			Main:        randomCondition,
			Description: strings.ToLower(randomCondition),
		},
	}
	
	return weatherData
}

// New command - Quote
func handleQuote(message Message) {
	quotes := []string{
		"Be yourself; everyone else is already taken. - Oscar Wilde",
		"Two things are infinite: the universe and human stupidity; and I'm not sure about the universe. - Albert Einstein",
		"You only live once, but if you do it right, once is enough. - Mae West",
		"Be the change that you wish to see in the world. - Mahatma Gandhi",
		"In three words I can sum up everything I've learned about life: it goes on. - Robert Frost",
		"If you tell the truth, you don't have to remember anything. - Mark Twain",
		"A friend is someone who knows all about you and still loves you. - Elbert Hubbard",
		"To be yourself in a world that is constantly trying to make you something else is the greatest accomplishment. - Ralph Waldo Emerson",
		"It is better to be hated for what you are than to be loved for what you are not. - André Gide",
		"We accept the love we think we deserve. - Stephen Chbosky",
	}
	
	rand.Seed(time.Now().UnixNano())
	quote := quotes[rand.Intn(len(quotes))]
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n📜 %s```", quote))
}

// New command - Stats
func handleStats(message Message) {
	statsMutex.Lock()
	uptime := time.Since(startTime)
	cmdHandled := commandsHandled
	msgLogged := messagesLogged
	statsMutex.Unlock()
	
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	
	stats := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"**Bot Statistics**\n" +
		"Uptime: %d days, %d hours, %d minutes\n" +
		"Commands handled: %d\n" +
		"Messages logged: %d\n" +
		"Memory usage: %.2f MB```",
		days, hours, minutes, cmdHandled, msgLogged,
		float64(getMemoryUsage())/1024/1024)
	
	sendMessage(message.ChannelID, stats)
}

// Helper function to get memory usage (returns bytes)
func getMemoryUsage() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

// Restoration of previously removed functions
func handleSay(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide something to say!```")
		return
	}
	
	content := strings.Join(args, " ")
	
	// Send the new message - this is a special case, we send the raw content
	sendMessage(message.ChannelID, content)
	
	// Note: The handleMessage function will delete the original command message
}

func handleClear(message Message, args []string) {
	count := 10 // Default
	
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			count = n
		}
		
		// Limit for safety
		if count > 100 {
			count = 100
		}
	}
	
	// Use the API to delete messages
	deletedCount := deleteMessages(message.ChannelID, count)
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🗑️ Deleted %d messages.```", deletedCount))
}

func handleAvatar(message Message) {
	avatarURL := fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png?size=1024", 
		message.Author.ID, message.Author.Avatar)
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n%s```", avatarURL))
}

func handleUserInfo(message Message) {
	// Build user info string
	info := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n" +
		"**User Information:**\n" +
		"🪪 ID: %s\n" +
		"👤 Username: %s\n" +
		"🤖 Bot: %t\n" +
		"📆 Account Created: Unknown```", // Discord doesn't provide creation date in basic message objects
		message.Author.ID, message.Author.Username, message.Author.Bot)
	
	sendMessage(message.ChannelID, info)
}

func handleCredits(message Message) {
	creditsText := "```ansi\n\u001b[0;36m[CREDITS]\u001b[0m\n\n" +
		"Bot created by: \u001b[0;35mEclipse\u001b[0m\n" +
		"Thanks for using RUNE Selfbot!\n" +
		"```"
	
	sendMessage(message.ChannelID, creditsText)
}

// handleAutoPressure manages the autopressure feature
func handleAutoPressure(message Message, args []string) {
	apMutex.Lock()
	defer apMutex.Unlock()

	fmt.Printf("AP command received with args: %v\n", args)
	fmt.Printf("Message content: %s\n", message.Content)

	// Check if user wants to stop autopressure
	if len(args) > 0 && strings.ToLower(args[0]) == "stop" {
		fmt.Println("Stop command detected")
		if apActive {
			// Get username if possible, otherwise just use ID
			// Try to fetch from cache or previous mentions if available
			apActive = false
			if apStopChan != nil {
				close(apStopChan)
				apStopChan = nil
			}
			sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nAutopressure on <@%s> stopped!```", apTargetID))
		} else {
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nAutopressure is not active.```")
		}
		return
	}

	var targetID string
	
	// Check if we have a direct user ID as an argument
	if len(args) > 0 {
		// Try to parse as a valid user ID (only numbers)
		if _, err := strconv.ParseUint(args[0], 10, 64); err == nil {
			fmt.Printf("Using direct user ID: %s\n", args[0])
			targetID = args[0]
		}
	}
	
	// If no direct ID, try to extract mention
	if targetID == "" {
		mentions := extractMentions(message.Content)
		fmt.Printf("Extracted mentions: %v\n", mentions)
		
		if len(mentions) > 0 {
			targetID = mentions[0]
		}
	}
	
	// If we still don't have a target, show error
	if targetID == "" {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease mention a user or provide a user ID to start autopressure.```")
		return
	}

	// Stop any existing autopressure
	if apActive {
		apActive = false
		if apStopChan != nil {
			close(apStopChan)
		}
	}

	// Start new autopressure
	apTargetID = targetID
	apActive = true
	apStopChan = make(chan bool)
	
	fmt.Printf("Starting autopressure on user ID: %s\n", apTargetID)
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nAutopressure started on <@%s>.```", apTargetID))
	
	// Start autopressure in a goroutine
	go runAutoPressure(message.ChannelID, apTargetID, apStopChan)
}

// runAutoPressure sends random words to the target user at regular intervals
func runAutoPressure(channelID, targetID string, stopChan chan bool) {
	// Start with a fast message rate (200ms = 5 messages/second)
	initialDelay := 200 * time.Millisecond
	fallbackDelay := 500 * time.Millisecond // 2 messages/second
	
	currentDelay := initialDelay
	ticker := time.NewTicker(currentDelay)
	defer ticker.Stop()
	
	rateLimitHits := 0
	
	fmt.Printf("Starting autopressure with initial delay of %v\n", currentDelay)

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			apMutex.Lock()
			if !apActive {
				apMutex.Unlock()
				return
			}
			
			// Get random word from the list
			randomWord := apWords[rand.Intn(len(apWords))]
			message := "# " + randomWord + " <@" + targetID + ">"
			
			apMutex.Unlock()
			
			// Send message and check if it failed due to rate limiting
			msgID := sendMessage(channelID, message)
			
			// If sendMessage returns empty string, likely hit rate limit
			if msgID == "" {
				rateLimitHits++
				
				// After detecting rate limits, slow down
				if rateLimitHits >= 2 && currentDelay != fallbackDelay {
					fmt.Println("Rate limit detected, slowing down autopressure")
					ticker.Stop()
					currentDelay = fallbackDelay
					ticker = time.NewTicker(currentDelay)
				}
				
				// Add a small pause to let rate limit reset
				time.Sleep(1 * time.Second)
			}
		}
	}
}

// extractMentions extracts user IDs from message mentions
func extractMentions(content string) []string {
	fmt.Printf("Extracting mentions from: %s\n", content)
	var mentions []string
	mentionRegex := regexp.MustCompile(`<@!?(\d+)>`)
	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	
	fmt.Printf("Regex matches: %v\n", matches)
	
	for _, match := range matches {
		if len(match) >= 2 {
			mentions = append(mentions, match[1])
		}
	}
	
	fmt.Printf("Extracted mentions: %v\n", mentions)
	return mentions
}

// Add new status command
func handleStatus(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide a status: online, idle, dnd, invisible```")
		return
	}

	status := strings.ToLower(args[0])
	var statusText string

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
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nInvalid status. Use online, idle, dnd, or invisible```")
		return
	}

	// Update status
	currentStatus = statusText
	if err := updateStatus(statusText); err != nil {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nError changing status: %s```", err.Error()))
		return
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nStatus updated to %s```", status))
}

// Function to update Discord status
func updateStatus(status string) error {
	payload := map[string]interface{}{
		"op": GatewayOpcodeStatusUpdate,
		"d": map[string]interface{}{
			"since": nil,
			"activities": []interface{}{},
			"status": status,
			"afk": false,
		},
	}

	if err := wsConn.WriteJSON(payload); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

// New joke command
func handleJoke(message Message) {
	// Using JokeAPI
	resp, err := http.Get("https://v2.jokeapi.dev/joke/Any?blacklistFlags=nsfw,religious,political,racist,sexist,explicit&type=single")
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nError fetching joke```")
		return
	}
	defer resp.Body.Close()

	var jokeResp struct {
		Joke string `json:"joke"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&jokeResp); err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nError parsing joke```")
		return
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n😂 %s```", jokeResp.Joke))
}

// Urban Dictionary definition struct
type UrbanDefinition struct {
	Definition  string `json:"definition"`
	Example     string `json:"example"`
	ThumbsUp    int    `json:"thumbs_up"`
	ThumbsDown  int    `json:"thumbs_down"`
}

// New urban dictionary command
func handleUrban(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide a term to look up```")
		return
	}

	term := strings.Join(args, " ")
	
	// Check cache first
	if defs, ok := urbanCache[term]; ok {
		if len(defs) > 0 {
			formatUrbanDefinition(message.ChannelID, term, defs[0])
			return
		}
	}

	// Fetch from API if not in cache
	apiURL := fmt.Sprintf("https://api.urbandictionary.com/v0/define?term=%s", url.QueryEscape(term))
	resp, err := http.Get(apiURL)
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nError connecting to Urban Dictionary```")
		return
	}
	defer resp.Body.Close()

	var result struct {
		List []UrbanDefinition `json:"list"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nError parsing Urban Dictionary results```")
		return
	}

	if len(result.List) == 0 {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nNo definitions found for \"%s\"```", term))
		return
	}

	// Cache the result
	urbanCache[term] = result.List
	
	// Send the first definition
	formatUrbanDefinition(message.ChannelID, term, result.List[0])
}

// Format Urban Dictionary definition
func formatUrbanDefinition(channelID, term string, def UrbanDefinition) {
	// Cleanup the definition and example by removing extra spaces and newlines
	definition := strings.ReplaceAll(def.Definition, "\r", "")
	definition = strings.ReplaceAll(definition, "\n", " ")
	
	example := ""
	if def.Example != "" {
		example = "\n\n*Example:*\n" + strings.ReplaceAll(def.Example, "\r", "")
		example = strings.ReplaceAll(example, "\n", " ")
	}

	// Truncate if too long
	if len(definition) > 800 {
		definition = definition[:800] + "..."
	}
	if len(example) > 300 {
		example = example[:300] + "..."
	}

	response := fmt.Sprintf("```ansi\n\u001b[0;36m[URBAN DICTIONARY]\u001b[0m\n\n" +
		"Term: \u001b[0;33m%s\u001b[0m\n\n" +
		"%s%s\n\n" +
		"👍 %d | 👎 %d```",
		term, definition, example, def.ThumbsUp, def.ThumbsDown)

	sendMessage(channelID, response)
}

// New coin flip command
func handleCoinFlip(message Message) {
	rand.Seed(time.Now().UnixNano())
	result := "Heads"
	if rand.Intn(2) == 1 {
		result = "Tails"
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🪙 Coin flip: %s```", result))
}

// New random fact command
func handleFact(message Message) {
	facts := []string{
		"A crocodile cannot stick its tongue out.",
		"A shrimp's heart is in its head.",
		"The Hawaiian alphabet has 12 letters.",
		"Turtles can breathe through their butts.",
		"In 1923, a jockey suffered a fatal heart attack but his horse finished and won the race, making him the first and only jockey to win a race after death.",
		"A group of flamingos is called a 'flamboyance'.",
		"Octopuses have three hearts and blue blood.",
		"The average person will spend six months of their life waiting for red lights to turn green.",
		"A jiffy is an actual unit of time: 1/100th of a second.",
		"The world's oldest known living tree is over 5,000 years old.",
		"Bananas are berries, but strawberries aren't.",
		"A day on Venus is longer than a year on Venus.",
		"Honey never spoils. Archaeologists have found pots of honey in ancient Egyptian tombs that are over 3,000 years old and still perfectly good to eat.",
		"The shortest war in history was between Britain and Zanzibar in 1896. Zanzibar surrendered after 38 minutes.",
		"A bolt of lightning is five times hotter than the surface of the sun.",
		"The inventor of the frisbee was turned into a frisbee after death.",
		"The world's largest desert is Antarctica, not the Sahara.",
		"Cows have best friends and get stressed when they are separated.",
		"The fingerprints of koalas are virtually indistinguishable from those of humans.",
		"You can't hum while holding your nose closed.",
	}
	
	rand.Seed(time.Now().UnixNano())
	fact := facts[rand.Intn(len(facts))]
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🧠 %s```", fact))
}

// Base64 encode command
func handleEncode(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide text to encode```")
		return
	}

	text := strings.Join(args, " ")
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔐 Encoded: %s```", encoded))
}

// Base64 decode command
func handleDecode(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide text to decode```")
		return
	}

	text := strings.Join(args, " ")
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Invalid base64 encoding```")
		return
	}
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔓 Decoded: %s```", string(decoded)))
}

func handleMemePhrase(message Message) {
	templates := []string{
		"When you %s but then %s.",
		"Me trying to %s while %s.",
		"POV: You’re about to %s and suddenly %s.",
		"Nobody:\nLiterally nobody:\nMe: %s while %s.",
		"Just another day of %s and %s.",
	}

	actions := []string{
		"touch grass", "debug spaghetti code", "drink coffee at 2AM",
		"google an error", "overthink everything", "forget your password",
		"rename final_final_v2", "accidentally close the terminal",
		"open 27 tabs", "write 'TODO' and forget forever",
	}

	rand.Seed(time.Now().UnixNano())
	t := templates[rand.Intn(len(templates))]
	a1 := actions[rand.Intn(len(actions))]
	a2 := actions[rand.Intn(len(actions))]

	phrase := fmt.Sprintf(t, a1, a2)

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n😂 %s```", phrase))
}


// Generate password command
func handlePassword(message Message, args []string) {
	length := 16 // Default password length
	
	if len(args) > 0 {
		if l, err := strconv.Atoi(args[0]); err == nil && l > 0 {
			length = l
			// Max reasonable length
			if length > 100 {
				length = 100
			}
		}
	}
	
	// Characters to use in password
	chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+[]{}|;:,.<>?"
	
	rand.Seed(time.Now().UnixNano())
	password := make([]byte, length)
	for i := 0; i < length; i++ {
		password[i] = chars[rand.Intn(len(chars))]
	}
	
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔑 Generated password (%d chars):\n%s```", length, string(password)))
}

// IP lookup command
func handleIPLookup(message Message, args []string) {
	var ip string
	
	if len(args) == 0 {
		// If no IP provided, get the user's IP
		ipInfo, err := getUserLocationFromIP()
		if err != nil {
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Error getting your IP information```")
			return
		}
		
		ip = ipInfo.IP
	} else {
		ip = args[0]
	}
	
	// Using ip-api.com for IP lookup
	apiURL := fmt.Sprintf("http://ip-api.com/json/%s", url.QueryEscape(ip))
	resp, err := http.Get(apiURL)
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Error connecting to IP lookup service```")
		return
	}
	defer resp.Body.Close()
	
	var result struct {
		Status      string  `json:"status"`
		Country     string  `json:"country"`
		CountryCode string  `json:"countryCode"`
		Region      string  `json:"region"`
		RegionName  string  `json:"regionName"`
		City        string  `json:"city"`
		Zip         string  `json:"zip"`
		Lat         float64 `json:"lat"`
		Lon         float64 `json:"lon"`
		Timezone    string  `json:"timezone"`
		ISP         string  `json:"isp"`
		Org         string  `json:"org"`
		AS          string  `json:"as"`
		Query       string  `json:"query"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Error parsing IP lookup result```")
		return
	}
	
	if result.Status != "success" {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ IP lookup failed for %s```", ip))
		return
	}
	
	response := fmt.Sprintf("```ansi\n\u001b[0;36m[IP LOOKUP]\u001b[0m\n\n" +
		"IP: \u001b[0;33m%s\u001b[0m\n" +
		"Location: %s, %s, %s\n" +
		"Coordinates: %f, %f\n" +
		"ISP: %s\n" +
		"Organization: %s\n" +
		"Timezone: %s\n" +
		"AS: %s```",
		result.Query,
		result.City, result.RegionName, result.Country,
		result.Lat, result.Lon,
		result.ISP,
		result.Org,
		result.Timezone,
		result.AS)
	
	sendMessage(message.ChannelID, response)
}

// New command - Random Tits
func handleTits(message Message) {
	// Send a temporary message
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔄 Finding boobies...```")
	
	// Make a request to the NekosAPI to get a random image with large_breasts tag
	resp, err := http.Get("https://api.nekosapi.com/v4/images/random?tags=large_breasts")
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to get image: Connection error```")
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ API returned error: %s```", resp.Status))
		return
	}
	
	// Parse the JSON response
	var images []struct {
		URL string `json:"url"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to parse response```")
		return
	}
	
	// Check if we got any valid images
	if len(images) == 0 {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ No images found```")
		return
	}
	
	// Pick a random image from the response
	rand.Seed(time.Now().UnixNano())
	randomIndex := rand.Intn(len(images))
	imageURL := images[randomIndex].URL
	
	// Send the image URL
	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🍒 Enjoy:```\n%s", imageURL))
}

// New command - PornHub Search
func handlePornhubSearch(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide a search term!```")
		return
	}
	
	// Create the search query by joining all arguments and URL encoding them
	searchQuery := url.QueryEscape(strings.Join(args, " "))
	
	// Create the PornHub search URL
	pornhubURL := fmt.Sprintf("https://www.pornhub.com/video/search?search=%s", searchQuery)
	
	// Send the search URL
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔍 PornHub Search:```\n%s", pornhubURL))
}

// URL shortener command using TinyURL API
func handleShortenURL(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nPlease provide a URL to shorten```")
		return
	}

	// Get the URL to shorten
	longURL := args[0]
	
	// Create a temporary message while we fetch the data
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔄 Shortening URL...```")
	
	// Use TinyURL API to shorten the URL
	apiURL := fmt.Sprintf("https://tinyurl.com/api-create.php?url=%s", url.QueryEscape(longURL))
	resp, err := http.Get(apiURL)
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Error connecting to URL shortener```")
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to shorten URL: service error```")
		return
	}
	
	// Read the shortened URL from the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to read response```")
		return
	}
	
	shortURL := string(body)
	
	// Send the shortened URL
	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🔗 Shortened URL:```\n%s", shortURL))
}

// RPC command handler for Discord Rich Presence
func handleRPC(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nRPC usage: "+config.Prefix+"rpc <on|off|status|set>```")
		return
	}
	
	switch args[0] {
	case "on":
		if err := startRPC(); err != nil {
			sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to start RPC: %v```", err))
		} else {
			config.RPC.Enabled = true
			if err := saveConfig(); err != nil {
				fmt.Printf("Error saving config: %v\n", err)
			}
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n✅ Discord Rich Presence enabled```")
		}
	case "off":
		stopRPC()
		config.RPC.Enabled = false
		if err := saveConfig(); err != nil {
			fmt.Printf("Error saving config: %v\n", err)
		}
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n✅ Discord Rich Presence disabled```")
	case "status":
		status := "disabled"
		if config.RPC.Enabled {
			status = "enabled"
		}
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nDiscord Rich Presence is currently %s```", status))
	case "set":
		if len(args) < 3 {
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nUsage: "+config.Prefix+"rpc set <field> <value>```")
			return
		}
		
		field := args[1]
		value := strings.Join(args[2:], " ")
		
		switch field {
		case "state":
			config.RPC.State = value
		case "details":
			config.RPC.Details = value
		case "largeimage":
			config.RPC.LargeImage = value
		case "largetext":
			config.RPC.LargeText = value
		case "appid":
			config.RPC.ApplicationID = value
		default:
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nInvalid field. Valid fields: state, details, largeimage, largetext, appid```")
			return
		}
		
		// Save the config
		if err := saveConfig(); err != nil {
			sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to save config: %v```", err))
			return
		}
		
		// Update RPC if enabled
		if config.RPC.Enabled {
			if err := updateRPC(); err != nil {
				sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n⚠️ Saved but failed to update RPC: %v```", err))
				return
			}
		}
		
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n✅ Updated RPC %s to: %s```", field, value))
	default:
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\nRPC usage: "+config.Prefix+"rpc <on|off|status|set>```")
	}
}

// Start Discord Rich Presence
func startRPC() error {
	fmt.Println("Starting Discord Rich Presence...")
	
	// Check if app ID is set
	if config.RPC.ApplicationID == "" {
		return fmt.Errorf("application ID not set")
	}
	
	// Create a new RPC client
	var err error
	rpcClient, err = discordrpc.New(config.RPC.ApplicationID)
	if err != nil {
		return fmt.Errorf("failed to create RPC client: %v", err)
	}
	
	// Set the activity
	return updateRPC()
}

// Update Discord Rich Presence
func updateRPC() error {
	if rpcClient == nil {
		return fmt.Errorf("RPC client not initialized")
	}
	
	fmt.Println("Updating Discord Rich Presence...")
	
	// Set the activity using the function from the discordrpc package
	err := rpcClient.SetActivity(
		config.RPC.State,
		config.RPC.Details,
		config.RPC.LargeImage,
		config.RPC.LargeText,
	)
	
	if err != nil {
		return fmt.Errorf("failed to set activity: %v", err)
	}
	
	return nil
}

// Stop Discord Rich Presence
func stopRPC() {
	fmt.Println("Stopping Discord Rich Presence...")
	
	// Close the RPC connection if it exists
	if rpcClient != nil {
		rpcClient.Close()
		rpcClient = nil
	}
}

// Save the config to disk
func saveConfig() error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling config: %v", err)
	}
	
	return os.WriteFile("config.json", data, 0644)
}

// New command - Random Cat
func handleCat(message Message) {
	// Send a temporary message while we fetch the image
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🐱 Fetching a cat...```")
	
	// Using The Cat API
	resp, err := http.Get("https://api.thecatapi.com/v1/images/search")
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to get content: Connection error```")
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ API returned error: %s```", resp.Status))
		return
	}
	
	// Structure for The Cat API JSON response
	var catResponse []struct {
		URL string `json:"url"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&catResponse); err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n❌ Failed to parse response```")
		return
	}
	
	// Check if we got any valid images
	if len(catResponse) == 0 {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🐱 Couldn't find a cat image```")
		return
	}
	
	// Send the image URL
	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n🐱 Enjoy:```\n%s", catResponse[0].URL))
}

func main() {
	fmt.Println("Starting Discord selfbot...")
	fmt.Printf("Using token: %s...\n", config.Token[:15])
	fmt.Printf("Owner ID: %.0f\n", config.OwnerID)
	fmt.Printf("Command prefix: %s\n", config.Prefix)
	
	// Initialize RPC if enabled in config
	if config.RPC.Enabled {
		fmt.Println("Initializing Discord Rich Presence...")
		if err := startRPC(); err != nil {
			fmt.Printf("Error initializing RPC: %v\n", err)
		} else {
			fmt.Println("Discord Rich Presence enabled")
		}
	}
	
	if err := connectWebsocket(); err != nil {
		fmt.Printf("Error connecting to gateway: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Println("Bot is now running. Press Ctrl+C to exit.")
	go listenForMessages()
	
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	
	if heartbeatTicker != nil {
		heartbeatTicker.Stop()
	}
	
	if wsConn != nil {
		wsConn.Close()
	}
	
	// Clean up RPC on exit
	stopRPC()
	
	fmt.Println("Shutting down...")
}