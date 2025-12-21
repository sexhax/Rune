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
)

type Config struct {
	Token               string `json:"token"`
	OwnerID             string `json:"OwnerID"`
	Prefix              string `json:"prefix"`
	GeminiAPIKey        string `json:"gemini_api_key"`
	AutoResponseEnabled bool   `json:"auto_response_enabled"`
	AutoResponsePhrase  string `json:"auto_response_phrase"`
}

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

type WSPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  int             `json:"s"`
	T  string          `json:"t"`
}

const (
	GatewayOpcodeDispatch            = 0
	GatewayOpcodeHeartbeat           = 1
	GatewayOpcodeIdentify            = 2
	GatewayOpcodeStatusUpdate        = 3
	GatewayOpcodeVoiceStateUpdate    = 4
	GatewayOpcodeVoiceServerPing     = 5
	GatewayOpcodeResume              = 6
	GatewayOpcodeReconnect           = 7
	GatewayOpcodeRequestGuildMembers = 8
	GatewayOpcodeInvalidSession      = 9
	GatewayOpcodeHello               = 10
	GatewayOpcodeHeartbeatACK        = 11
)

const (
	StatusOnline    = "online"
	StatusIdle      = "idle"
	StatusDND       = "dnd"
	StatusInvisible = "invisible"
)

var (
	config          Config
	wsConn          *websocket.Conn
	heartbeatTicker *time.Ticker
	sequence        int
	sessionID       string
	lastHeartbeat   time.Time
	lastMessageID   string
	startTime       = time.Now()

	messagesLogged  int
	commandsHandled int
	statsMutex      sync.Mutex

	autoResponderEnabled bool
	autoResponderMutex   sync.Mutex

	autoPressureTargets = make(map[string]chan bool)
	apMutex             sync.Mutex
	apActive            bool
	apStopChan          chan bool
	apTargetID          string
	apWords             = []string{"je kanker vader", "Ratio", "Clown", "Bozo", "Failure", "Trash", "Cry", "Skill issue", "nigger", "ape", "monkey boy", "ching chong"}

	currentStatus = StatusOnline

	urbanCache = make(map[string][]UrbanDefinition)
)

func init() {
	configFile, err := os.ReadFile("config.json")
	if err != nil {
		fmt.Println("Error reading config file:", err)
		if os.IsNotExist(err) {
			config = Config{
				Token:        "YOUR_TOKEN_HERE",
				OwnerID:      "",
				Prefix:       "&",
				GeminiAPIKey: "YOUR_GEMINI_API_KEY",
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

	if config.Token == "" || config.Token == "YOUR_TOKEN_HERE" {
		fmt.Println("Please set your Discord token in config.json")
		os.Exit(1)
	}

	if config.GeminiAPIKey == "" || config.GeminiAPIKey == "YOUR_GEMINI_API_KEY" {
		fmt.Println("Please set your Gemini API key in config.json for the &ai command")
	}

	rand.Seed(time.Now().UnixNano())
}

type IPGeolocation struct {
	IP        string  `json:"ip"`
	City      string  `json:"city"`
	Region    string  `json:"region"`
	Country   string  `json:"country"`
	Loc       string  `json:"loc"`
	Timezone  string  `json:"timezone"`
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

func triggerTyping(channelID string) {
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("https://discord.com/api/v10/channels/%s/typing", channelID),
		nil,
	)
	if err != nil {
		return
	}

	req.Header.Set("Authorization", config.Token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/json")


	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		fmt.Printf("Typing failed, status: %d\n", resp.StatusCode)
		return
	}

	delay := 2000 + rand.Intn(500) 
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

func connectWebsocket() error {
	gatewayURL, err := getGatewayURL()
	if err != nil {
		return fmt.Errorf("failed to get gateway URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(gatewayURL+"/?v=10&encoding=json", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}

	wsConn = conn

	var payload WSPayload
	if err := wsConn.ReadJSON(&payload); err != nil {
		return fmt.Errorf("failed to read hello: %w", err)
	}

	if payload.Op != GatewayOpcodeHello {
		return fmt.Errorf("expected hello op code, got %d", payload.Op)
	}

	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(payload.D, &helloData); err != nil {
		return fmt.Errorf("failed to parse hello data: %w", err)
	}

	go startHeartbeat(helloData.HeartbeatInterval)

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
			if err := connectWebsocket(); err != nil {
				fmt.Printf("Failed to reconnect: %v\n", err)
			}
			return
		}
	}
}

func listenForMessages() {
	for {
		var payload WSPayload
		if err := wsConn.ReadJSON(&payload); err != nil {
			fmt.Printf("Error reading from websocket: %v\n", err)

			if err := connectWebsocket(); err != nil {
				fmt.Printf("Failed to reconnect: %v\n", err)
				time.Sleep(5 * time.Second)
			}
			continue
		}

		if payload.S != 0 {
			sequence = payload.S
		}

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

				messageTime, err := time.Parse(time.RFC3339, message.Timestamp)
				if err != nil || !messageTime.After(startTime) {
					continue
				}

				if !message.Author.Bot {
					fmt.Printf("Message from %s: %s\n", message.Author.Username, message.Content)

					statsMutex.Lock()
					messagesLogged++
					statsMutex.Unlock()

					ownerIDStr := config.OwnerID
					if message.Author.ID != ownerIDStr && autoResponderEnabled {
						selfMentioned := false
						for _, mention := range message.Mentions {
							if mention.ID == ownerIDStr {
								selfMentioned = true
								break
							}
						}

						if !selfMentioned && strings.Contains(message.Content, "<@"+ownerIDStr+">") {
							selfMentioned = true
						}

						if selfMentioned {
							fmt.Printf("Autoresponder triggered by %s\n", message.Author.Username)
							response := config.AutoResponsePhrase
							if response == "" {
								response = "I'm currently unavailable. Please try again later."
							}
							response = strings.ReplaceAll(response, "<user>", message.Author.Username)
							autoResponse := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n%s```", response)
							sendMessage(message.ChannelID, autoResponse)
						}
					}
				}

				ownerIDStr := config.OwnerID
				fmt.Printf("Message author ID: %s, Owner ID: %s\n", message.Author.ID, ownerIDStr)

				if message.Author.ID == ownerIDStr || message.Author.Username == "ndq2" {
					fmt.Printf("Owner command detected: %s\n", message.Content)
					if strings.HasPrefix(message.Content, config.Prefix) {
    statsMutex.Lock()
    commandsHandled++
    statsMutex.Unlock()

    // Trigger typing indicator and simulate human delay
    go func(channelID string) {
        triggerTyping(channelID)

        // Random delay between 2.0 and 2.5 seconds
        delay := 2000 + rand.Intn(501) // 2000-2500 ms
        time.Sleep(time.Duration(delay) * time.Millisecond)

        // Now process the command after "typing"
        handleMessage(message)
    }(message.ChannelID)

    // Do not call handleMessage directly — it's now async after typing
    continue // Skip further processing for this message
}
				} else {
					fmt.Printf("Message not from owner! Author: %s (ID: %s), Owner: %s\n",
						message.Author.Username, message.Author.ID, ownerIDStr)
				}
			}

		case GatewayOpcodeHeartbeatACK:
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

func sendMessage(channelID, content string) string {
	fmt.Printf("Attempting to send message to channel %s: %s\n", channelID, content)

	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	fmt.Printf("POST URL: %s\n", url)

	reqBody := map[string]string{
		"content": content,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return ""
	}

	fmt.Printf("Request body: %s\n", string(jsonBody))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		fmt.Println("Error creating request:", err)
		return ""
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", config.Token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error sending message:", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error sending message: %s (status code: %d)\nResponse: %s\n",
			resp.Status, resp.StatusCode, string(body))
		return ""
	} else {
		fmt.Printf("Message sent successfully to channel %s\n", channelID)

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

func editMessage(channelID, messageID, newContent string) bool {
	if messageID == "" {
		fmt.Println("Cannot edit message: messageID is empty")
		return false
	}

	fmt.Printf("Attempting to edit message %s in channel %s\n", messageID, channelID)

	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages/%s", channelID, messageID)

	reqBody := map[string]string{
		"content": newContent,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Println("Error marshaling JSON for edit:", err)
		return false
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		fmt.Println("Error creating edit request:", err)
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", config.Token)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error editing message:", err)
		return false
	}
	defer resp.Body.Close()

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

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		fmt.Println("Error creating delete request:", err)
		return false
	}

	req.Header.Set("Authorization", config.Token)

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
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages?limit=%d", channelID, count)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println("Error creating request for message history:", err)
		return 0
	}

	req.Header.Set("Authorization", config.Token)

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println("Error getting message history:", err)
		return 0
	}

	var messages []struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		fmt.Println("Error parsing message history:", err)
		resp.Body.Close()
		return 0
	}
	resp.Body.Close()

	deletedCount := 0
	for _, msg := range messages {
		if deleteMessage(channelID, msg.ID) {
			deletedCount++

			time.Sleep(time.Millisecond * 300)
		}
	}

	return deletedCount
}

func handleMessage(message Message) {
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

	fmt.Printf("Processing command: %s with args: %v\n", command, args)

	statsMutex.Lock()
	commandsHandled++
	statsMutex.Unlock()

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
	case "ai":
		handleAI(message)
	case "nsfw":
		fmt.Println("Executing NSFW command...")
		handleNSFW(message)
	case "psearch":
		fmt.Println("Executing pornhubsearch command...")
		handlePornhubSearch(message, args)
	case "google":
		fmt.Println("Executing google...")
		handleGoogleSearch(message, args)
	case "tits":
		fmt.Println("Executing titty command...")
		handleTits(message)
	case "catgirl":
		fmt.Println("Executing titty command...")
		handleCatgirl(message)
	case "ping":
		fmt.Println("Executing ping command...")
		handlePing(message)
	case "setprefix":
		fmt.Println("Executing setprefix command...")
		handleSetPrefix(message, args)
	case "say":
		fmt.Println("Executing say command...")
		handleSay(message, args)
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
	case "setphrase":
		fmt.Println("Executing setphrase command...")
		handleSetPhrase(message, args)
	default:
		fmt.Printf("Unknown command: %s\n", command)
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nUnknown command: `%s`. Type %shelp for a list of commands.```", command, config.Prefix))
	}

	if deleted := deleteMessage(message.ChannelID, message.ID); deleted {
		fmt.Printf("Deleted command message: %s\n", message.ID)
	} else {
		fmt.Printf("Failed to delete command message: %s\n", message.ID)
	}

	fmt.Printf("Command processing completed for: %s\n", command)
}

func handleHelp(message Message) {
	helpText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n" +
		"Commands:\n" +
		"\u001b[0;32m" + config.Prefix + "help\u001b[0m - Show this help message\n" +
		"\u001b[0;32m" + config.Prefix + "categories\u001b[0m - Show all command categories\n" +
		"\u001b[0;32m" + config.Prefix + "utilities\u001b[0m - Show utility commands\n" +
		"\u001b[0;32m" + config.Prefix + "fun\u001b[0m - Show fun commands\n" +
		"\u001b[0;32m" + config.Prefix + "info\u001b[0m - Show information commands\n" +
		"\u001b[0;33m" + config.Prefix + "NSFW\u001b[0m - Not safe for work \n" +
		"Tip: Type " + config.Prefix + "help <command> for detailed help on a specific command\n" +
		"```"
	sendMessage(message.ChannelID, helpText)
}

func handleCategories(message Message) {
	categoriesText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n" +
		"Command Categories:\n\n" +
		"\u001b[0;33mUtilities\u001b[0m - Useful tools and functions\n" +
		"\u001b[0;33mFun\u001b[0m - Entertainment and random commands\n" +
		"\u001b[0;33mInfo\u001b[0m - Information and statistics\n\n" +
		"\u001b[0;33mNSFW\u001b[0m - Not safe for work\n\n" +
		"Use " + config.Prefix + "<category> to see commands in each category\n" +
		"```"
	sendMessage(message.ChannelID, categoriesText)
}

func handleUtilities(message Message) {
	utilitiesText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n" +
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
		"\u001b[0;32m" + config.Prefix + "ai [prompt]\u001b[0m - Get ai results\n" +
		"\u001b[0;32m" + config.Prefix + "shorten <url>\u001b[0m - Shorten a URL\n" +
		"\u001b[0;32m" + config.Prefix + "setprefix [prefix]\u001b[0m - changes prefix\n" +
		"\u001b[0;32m" + config.Prefix + "cloneserver\u001b[0m - Clone a Discord server\n" +
		"```"
	sendMessage(message.ChannelID, utilitiesText)
}

func handleFun(message Message) {
	funText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n" +
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
		"\u001b[0;32m" + config.Prefix + "roast [@user]\u001b[0m - Roast someone\n" +
		"\u001b[0;32m" + config.Prefix + "dadjoke\u001b[0m - Get a dad joke\n" +
		"\u001b[0;32m" + config.Prefix + "compliment [@user]\u001b[0m - Compliment someone\n" +
		"\u001b[0;32m" + config.Prefix + "cat\u001b[0m - Get a random cat picture\n" +
		"\u001b[0;32m" + config.Prefix + "psearch <term>\u001b[0m - Search PornHub for videos\n" +
		"\u001b[0;32m" + config.Prefix + "tits\u001b[0m - Get a random tits image\n" +
		"\u001b[0;32m" + config.Prefix + "meme\u001b[0m - Get a random meme\n" +
		"```"
	sendMessage(message.ChannelID, funText)
}

func handleNSFW(message Message) {
	funText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n\n" +
		"\u001b[0;33mNSFW Commands:\u001b[0m\n\n" +
		"\u001b[0;32m" + config.Prefix + "psearch <term>\u001b[0m - Search PornHub for videos\n" +
		"\u001b[0;32m" + config.Prefix + "tits\u001b[0m - Get a random tits image\n" +
		"\u001b[0;32m" + config.Prefix + "catgirl\u001b[0m - Get a random catgirl image\n" +
		"```"
	sendMessage(message.ChannelID, funText)
}

func handleInfo(message Message) {
	infoText := "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n" +
		"\u001b[0;33mInfo Commands:\u001b[0m\n\n" +
		"\u001b[0;32m" + config.Prefix + "whoami\u001b[0m - Show your user info\n" +
		"\u001b[0;32m" + config.Prefix + "avatar\u001b[0m - Get your avatar URL\n" +
		"\u001b[0;32m" + config.Prefix + "stats\u001b[0m - Show bot statistics\n" +
		"\u001b[0;32m" + config.Prefix + "credits\u001b[0m - Display bot credits\n" +
		"```"
	sendMessage(message.ChannelID, infoText)
}

func handlePing(message Message) {
	start := time.Now()

	req, _ := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	req.Header.Set("Authorization", config.Token)
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError calculating ping: connection failed```")
		return
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🏓 Pong! Latency: %dms```", latency))
}

func handleAutoResponder(message Message) {
	autoResponderMutex.Lock()
	autoResponderEnabled = !autoResponderEnabled
	status := "enabled"
	if !autoResponderEnabled {
		status = "disabled"
	}
	autoResponderMutex.Unlock()

	fmt.Printf("Auto responder %s\n", status)
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nAuto responder %s```", status))
}

func handleFemboy(message Message, args []string) {
	var targetUsername string

	if len(message.Mentions) > 0 {
		targetUsername = message.Mentions[0].Username
	} else if len(args) > 0 {
		mentions := extractMentions(message.Content)
		if len(mentions) > 0 {
			targetUsername = "user"
		} else {
			targetUsername = strings.Join(args, " ")
		}
	} else {
		targetUsername = "You"
	}

	rand.Seed(time.Now().UnixNano())
	percentage := rand.Intn(101)

	response := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n%s, you are %d%% femboy :3```", targetUsername, percentage)

	sendMessage(message.ChannelID, response)
}

func handle8Ball(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease ask a question!```")
		return
	}

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

	question := strings.Join(args, " ")
	if !strings.HasSuffix(question, "?") {
		question += "?"
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n%s\n\n🎱 %s```", question, response))
}

func handleRoll(message Message, args []string) {
	sides := 6

	if len(args) > 0 {
		if s, err := strconv.Atoi(args[0]); err == nil && s > 0 {
			sides = s
		}
	}

	rand.Seed(time.Now().UnixNano())
	result := rand.Intn(sides) + 1

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🎲 You rolled a %d (d%d)```", result, sides))
}

func handleRizz(message Message, args []string) {
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔄 Fetching rizz line...```")

	type RizzResponse struct {
		ID       string `json:"_id"`
		Text     string `json:"text"`
		Language string `json:"language"`
	}

	var line string
	maxRetries := 5

	for i := 0; i < maxRetries; i++ {
		resp, err := http.Get("https://rizzapi.vercel.app/random")
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var rizzResponse RizzResponse
		if err := json.NewDecoder(resp.Body).Decode(&rizzResponse); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		if rizzResponse.Language == "English" {
			line = rizzResponse.Text
			break
		}
	}

	if line == "" {
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

	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❤️ %s```", line))
}

func getLocationFromIP() (*IPGeolocation, error) {
	resp, err := http.Get("https://ipinfo.io/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get IP info: %s", resp.Status)
	}

	var geolocation IPGeolocation
	if err := json.NewDecoder(resp.Body).Decode(&geolocation); err != nil {
		return nil, err
	}

	if geolocation.Loc != "" {
		coords := strings.Split(geolocation.Loc, ",")
		if len(coords) == 2 {
			geolocation.Latitude, _ = strconv.ParseFloat(coords[0], 64)
			geolocation.Longitude, _ = strconv.ParseFloat(coords[1], 64)
		}
	}

	return &geolocation, nil
}

func getWeatherData(lat, lon float64) (*WeatherData, error) {
	apiKey := "9de243494c0b295cca9337e1e96b00e2"
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

func handleWeather(message Message) {
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
		ipInfo, err := getUserLocationFromIP()
		if err != nil {
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

	weatherData, err := getWeatherData(lat, lon)
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "❌ Error fetching weather: "+err.Error())
		return
	}

	weatherMsg := formatWeatherMessage(weatherData, locationName)
	editMessage(message.ChannelID, statusMsgID, weatherMsg)
}

type GeocodingResponse []struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Country string  `json:"country"`
	State   string  `json:"state,omitempty"`
}

type IPInfo struct {
	IP      string  `json:"ip"`
	City    string  `json:"city"`
	Region  string  `json:"region"`
	Country string  `json:"country"`
	Lat     float64 `json:"latitude"`
	Lon     float64 `json:"longitude"`
}

func getLocationCoordinates(location string) (GeocodingResponse, error) {
	apiKey := "9de243494c0b295cca9337e1e96b00e2"
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

func getRandomWeather() *WeatherData {
	conditions := []string{"Clear", "Clouds", "Rain", "Thunderstorm", "Snow", "Mist"}
	randomCondition := conditions[rand.Intn(len(conditions))]

	weatherData := &WeatherData{}
	weatherData.Main.Temp = float64(rand.Intn(35)) - 5
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

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n📜 %s```", quote))
}

func handleStats(message Message) {
	statsMutex.Lock()
	uptime := time.Since(startTime)
	cmdHandled := commandsHandled
	msgLogged := messagesLogged
	statsMutex.Unlock()

	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60

	stats := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n"+
		"Bot Statistics\n"+
		"Uptime: %d days, %d hours, %d minutes\n"+
		"Commands handled: %d\n"+
		"Messages logged: %d\n"+
		"Memory usage: %.2f MB```",
		days, hours, minutes, cmdHandled, msgLogged,
		float64(getMemoryUsage())/1024/1024)

	sendMessage(message.ChannelID, stats)
}

func getMemoryUsage() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

func handleSay(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide something to say!```")
		return
	}

	content := strings.Join(args, " ")

	sendMessage(message.ChannelID, content)
}

func handleClear(message Message, args []string) {
	count := 10

	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			count = n
		}

		if count > 100 {
			count = 100
		}
	}

	deletedCount := deleteMessages(message.ChannelID, count)

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🗑️ Deleted %d messages.```", deletedCount))
}

func handleAvatar(message Message) {
	avatarURL := fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png?size=1024",
		message.Author.ID, message.Author.Avatar)

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n%s```", avatarURL))
}

func handleUserInfo(message Message) {
	info := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n"+
		"User Information:\n"+
		"🪪 ID: %s\n"+
		"👤 Username: %s\n"+
		"🤖 Bot: %t\n"+
		"📆 Account Created: Unknown```",
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

func handleAutoPressure(message Message, args []string) {
	apMutex.Lock()
	defer apMutex.Unlock()

	fmt.Printf("AP command received with args: %v\n", args)
	fmt.Printf("Message content: %s\n", message.Content)

	if len(args) > 0 && strings.ToLower(args[0]) == "stop" {
		fmt.Println("Stop command detected")
		if apActive {
			apActive = false
			if apStopChan != nil {
				close(apStopChan)
				apStopChan = nil
			}
			sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nAutopressure on <@%s> stopped!```", apTargetID))
		} else {
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nAutopressure is not active.```")
		}
		return
	}

	var targetID string

	if len(args) > 0 {
		if _, err := strconv.ParseUint(args[0], 10, 64); err == nil {
			fmt.Printf("Using direct user ID: %s\n", args[0])
			targetID = args[0]
		}
	}

	if targetID == "" {
		mentions := extractMentions(message.Content)
		fmt.Printf("Extracted mentions: %v\n", mentions)

		if len(mentions) > 0 {
			targetID = mentions[0]
		}
	}

	if targetID == "" {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease mention a user or provide a user ID to start autopressure.```")
		return
	}

	if apActive {
		apActive = false
		if apStopChan != nil {
			close(apStopChan)
		}
	}

	apTargetID = targetID
	apActive = true
	apStopChan = make(chan bool)

	fmt.Printf("Starting autopressure on user ID: %s\n", apTargetID)
	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nAutopressure started on <@%s>.```", apTargetID))

	go runAutoPressure(message.ChannelID, apTargetID, apStopChan)
}

func runAutoPressure(channelID, targetID string, stopChan chan bool) {
	initialDelay := 200 * time.Millisecond
	fallbackDelay := 500 * time.Millisecond

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

			randomWord := apWords[rand.Intn(len(apWords))]
			message := "# " + randomWord + " <@" + targetID + ">"

			apMutex.Unlock()

			msgID := sendMessage(channelID, message)

			if msgID == "" {
				rateLimitHits++

				if rateLimitHits >= 2 && currentDelay != fallbackDelay {
					fmt.Println("Rate limit detected, slowing down autopressure")
					ticker.Stop()
					currentDelay = fallbackDelay
					ticker = time.NewTicker(currentDelay)
				}

				time.Sleep(1 * time.Second)
			}
		}
	}
}

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

func handleStatus(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide a status: online, idle, dnd, invisible```")
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
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nInvalid status. Use online, idle, dnd, or invisible```")
		return
	}

	currentStatus = statusText
	if err := updateStatus(statusText); err != nil {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError changing status: %s```", err.Error()))
		return
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nStatus updated to %s```", status))
}

func updateStatus(status string) error {
	payload := map[string]interface{}{
		"op": GatewayOpcodeStatusUpdate,
		"d": map[string]interface{}{
			"since":      nil,
			"activities": []interface{}{},
			"status":     status,
			"afk":        false,
		},
	}

	if err := wsConn.WriteJSON(payload); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

func handleJoke(message Message) {
	resp, err := http.Get("https://v2.jokeapi.dev/joke/Any?blacklistFlags=nsfw,religious,political,racist,sexist,explicit&type=single")
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError fetching joke```")
		return
	}
	defer resp.Body.Close()

	var jokeResp struct {
		Joke string `json:"joke"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&jokeResp); err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError parsing joke```")
		return
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n😂 %s```", jokeResp.Joke))
}

type UrbanDefinition struct {
	Definition string `json:"definition"`
	Example    string `json:"example"`
	ThumbsUp   int    `json:"thumbs_up"`
	ThumbsDown int    `json:"thumbs_down"`
}

func handleUrban(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide a term to look up```")
		return
	}

	term := strings.Join(args, " ")

	if defs, ok := urbanCache[term]; ok {
		if len(defs) > 0 {
			formatUrbanDefinition(message.ChannelID, term, defs[0])
			return
		}
	}

	apiURL := fmt.Sprintf("https://api.urbandictionary.com/v0/define?term=%s", url.QueryEscape(term))
	resp, err := http.Get(apiURL)
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError connecting to Urban Dictionary```")
		return
	}
	defer resp.Body.Close()

	var result struct {
		List []UrbanDefinition `json:"list"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError parsing Urban Dictionary results```")
		return
	}

	if len(result.List) == 0 {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nNo definitions found for \"%s\"```", term))
		return
	}

	urbanCache[term] = result.List

	formatUrbanDefinition(message.ChannelID, term, result.List[0])
}

func formatUrbanDefinition(channelID, term string, def UrbanDefinition) {
	definition := strings.ReplaceAll(def.Definition, "\r", "")
	definition = strings.ReplaceAll(definition, "\n", " ")

	example := ""
	if def.Example != "" {
		example = "\n\n*Example:*\n" + strings.ReplaceAll(def.Example, "\r", "")
		example = strings.ReplaceAll(example, "\n", " ")
	}

	if len(definition) > 800 {
		definition = definition[:800] + "..."
	}
	if len(example) > 300 {
		example = example[:300] + "..."
	}

	response := fmt.Sprintf("```ansi\n\u001b[0;36m[URBAN DICTIONARY]\u001b[0m\n\n"+
		"Term: \u001b[0;33m%s\u001b[0m\n\n"+
		"%s%s\n\n"+
		"👍 %d | 👎 %d```",
		term, definition, example, def.ThumbsUp, def.ThumbsDown)

	sendMessage(channelID, response)
}

func handleCoinFlip(message Message) {
	rand.Seed(time.Now().UnixNano())
	result := "Heads"
	if rand.Intn(2) == 1 {
		result = "Tails"
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🪙 Coin flip: %s```", result))
}

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

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🧠 %s```", fact))
}

func handleEncode(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide text to encode```")
		return
	}

	text := strings.Join(args, " ")
	encoded := base64.StdEncoding.EncodeToString([]byte(text))

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔐 Encoded: %s```", encoded))
}

func handleDecode(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide text to decode```")
		return
	}

	text := strings.Join(args, " ")
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Invalid base64 encoding```")
		return
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔓 Decoded: %s```", string(decoded)))
}

func handleMemePhrase(message Message) {
	templates := []string{
		"When you %s but then %s.",
		"Me trying to %s while %s.",
		"POV: You’re about to %s and suddenly %s.",
		"Nobody:\nLiterally nobody:\nMe: %s while %s.",
		"Just another day of %s and %s.",
		"Just finishing up %s and %s happens.",
	}

	actions := []string{
		"touch grass", "debug spaghetti code", "drink coffee at 2AM",
		"google an error", "overthink everything", "forget your password",
		"rename final_final_v2", "accidentally close the terminal",
		"open 27 tabs", "write 'TODO' and forget forever", "hunt niggers in the forest", "touching grass",
	}

	rand.Seed(time.Now().UnixNano())
	t := templates[rand.Intn(len(templates))]
	a1 := actions[rand.Intn(len(actions))]
	a2 := actions[rand.Intn(len(actions))]

	phrase := fmt.Sprintf(t, a1, a2)

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n😂 %s```", phrase))
}

func handlePassword(message Message, args []string) {
	length := 16

	if len(args) > 0 {
		if l, err := strconv.Atoi(args[0]); err == nil && l > 0 {
			length = l
			if length > 100 {
				length = 100
			}
		}
	}

	chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+[]{}|;:,.<>?"

	rand.Seed(time.Now().UnixNano())
	password := make([]byte, length)
	for i := 0; i < length; i++ {
		password[i] = chars[rand.Intn(len(chars))]
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔑 Generated password (%d chars):\n%s```", length, string(password)))
}

func handleSetPrefix(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nCurrent prefix: %s\nUse '&setprefix off' to disable prefix or '&setprefix !' to set a new symbol prefix```", config.Prefix))
		return
	}

	newPrefix := args[0]

	if strings.ToLower(newPrefix) == "off" {
		newPrefix = ""
	} else if len(newPrefix) != 1 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Prefix must be a single symbol character or 'off'```")
		return
	}

	oldPrefix := config.Prefix

	config.Prefix = newPrefix

	if err := saveConfig(); err != nil {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Error saving new prefix: %s```", err.Error()))
		config.Prefix = oldPrefix
		return
	}

	if newPrefix == "" {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n✅ Prefix disabled. Commands can now be used without a prefix```")
	} else {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n✅ Prefix changed from '%s' to '%s'```", oldPrefix, newPrefix))
	}
}

func handleSetPhrase(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nCurrent phrase: %s\nUse '&setphrase new phrase' to set a new phrase```", config.AutoResponsePhrase))
		return
	}

	ragingDemon := strings.Join(args, " ")

	oldDemon := config.AutoResponsePhrase

	config.AutoResponsePhrase = ragingDemon

	if err := saveConfig(); err != nil {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Error saving new phrase: %s```", err.Error()))
		config.AutoResponsePhrase = oldDemon
		return
	}

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n✅ Phrase changed from '%s' to '%s'```", oldDemon, ragingDemon))
}

func handleIPLookup(message Message, args []string) {
	var ip string

	if len(args) == 0 {
		ipInfo, err := getUserLocationFromIP()
		if err != nil {
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Error getting your IP information```")
			return
		}

		ip = ipInfo.IP

	} else {
		ip = args[0]
	}

	apiURL := fmt.Sprintf("http://ip-api.com/json/%s", url.QueryEscape(ip))
	resp, err := http.Get(apiURL)
	if err != nil {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Error connecting to IP lookup service```")
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
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Error parsing IP lookup result```")
		return
	}

	if result.Status != "success" {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ IP lookup failed for %s```", ip))
		return
	}

	response := fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m\n\n"+
		"IP: \u001b[0;33m%s\u001b[0m\n"+
		"Location: %s, %s, %s\n"+
		"Coordinates: %f, %f\n"+
		"ISP: %s\n"+
		"Organization: %s\n"+
		"Timezone: %s\n"+
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

func handleTits(message Message) {
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔄 Finding boobies...```")

	resp, err := http.Get("https://api.nekosapi.com/v4/images/random?tags=large_breasts")
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Failed to get image: Connection error```")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ API returned error: %s```", resp.Status))
		return
	}

	var images []struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Failed to parse response```")
		return
	}

	if len(images) == 0 {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ No images found```")
		return
	}

	rand.Seed(time.Now().UnixNano())
	randomIndex := rand.Intn(len(images))
	imageURL := images[randomIndex].URL

	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🍒 Enjoy:```\n%s", imageURL))
}

func handleCatgirl(message Message) {
	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔄 Finding catgirls...```")

	resp, err := http.Get("https://api.nekosapi.com/v4/images/random?tags=catgirl,large_breasts,exposed_girl_breasts")
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Failed to get image: Connection error```")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ API returned error: %s```", resp.Status))
		return
	}

	var images []struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Failed to parse response```")
		return
	}

	if len(images) == 0 {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ No images found```")
		return
	}

	rand.Seed(time.Now().UnixNano())
	randomIndex := rand.Intn(len(images))
	imageURL := images[randomIndex].URL

	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🍒 Enjoy:```\n%s", imageURL))
}

func handlePornhubSearch(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide a search term!```")
		return
	}

	searchQuery := url.QueryEscape(strings.Join(args, " "))

	pornhubURL := fmt.Sprintf("https://www.pornhub.com/video/search?search=%s", searchQuery)

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔍 PornHub Search:```\n%s", pornhubURL))
}

func handleGoogleSearch(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide a search term!```")
		return
	}

	searchQuery := url.QueryEscape(strings.Join(args, " "))

	googleURL := fmt.Sprintf("https://www.google.com/search?q=%s", searchQuery)

	sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔍 Google Search:```\n%s", googleURL))
}

func handleShortenURL(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide a URL to shorten```")
		return
	}

	longURL := args[0]

	statusMsgID := sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔄 Shortening URL...```")

	apiURL := fmt.Sprintf("https://tinyurl.com/api-create.php?url=%s", url.QueryEscape(longURL))
	resp, err := http.Get(apiURL)
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Error connecting to URL shortener```")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Failed to shorten URL: service error```")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		editMessage(message.ChannelID, statusMsgID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n❌ Failed to read response```")
		return
	}

	shortURL := string(body)

	editMessage(message.ChannelID, statusMsgID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n🔗 Shortened URL:```\n%s", shortURL))
}

func handleAI(message Message) {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nFunction removed due to gemini being a gay retard.```")
		return
}

/* func handleAI(message Message, args []string) {
	if len(args) == 0 {
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nPlease provide a prompt for the AI to respond to.```")
		return
	}

	prompt := strings.Join(args, " ")

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  "AIzaSyBMZ1H1gxXVZX47swfKlA_AiSHrvD3NsQc",
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Printf("Error initializing Gemini API client: %v", err)
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError initializing AI client.```")
		return
	}

	result, err := client.Models.GenerateContent(
		ctx,
		"gemini-2.0-flash",
		genai.Text(prompt),
		nil,
	)
	if err != nil {
		log.Printf("Error generating AI response: %v", err)
		sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError generating AI response.```")
		return
	}

	response := result.Text()

	if len(response) > 1999 {
		tempFile := fmt.Sprintf("ai_response_%d.txt", time.Now().Unix())
		err := os.WriteFile(tempFile, []byte(response), 0644)
		if err != nil {
			log.Printf("Error writing response to file: %v", err)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Response too long and couldn't save to file.```")
			return
		}

		file, err := os.Open(tempFile)
		if err != nil {
			log.Printf("Error opening response file: %v", err)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Couldn't read response file.```")
			return
		}
		defer file.Close()

		url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", message.ChannelID)
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		part, err := writer.CreateFormFile("file", tempFile)
		if err != nil {
			log.Printf("Error creating form file: %v", err)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Couldn't prepare file for upload.```")
			return
		}
		_, err = io.Copy(part, file)
		if err != nil {
			log.Printf("Error copying file to request: %v", err)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Couldn't prepare file content.```")
			return
		}
		writer.Close()

		req, err := http.NewRequest("POST", url, body)
		if err != nil {
			log.Printf("Error creating upload request: %v", err)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Couldn't create upload request.```")
			return
		}

		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", config.Token)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error uploading file: %v", err)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Couldn't upload response file.```")
			return
		}
		defer resp.Body.Close()

		os.Remove(tempFile)

		if resp.StatusCode != http.StatusOK {
			log.Printf("Error response from Discord: %v", resp.Status)
			sendMessage(message.ChannelID, "```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\nError: Couldn't send response file.```")
			return
		}
	} else {
		sendMessage(message.ChannelID, fmt.Sprintf("```ansi\n\u001b[0;36m[RUNE]\u001b[0m``````ansi\n%s```", response))
	}
} */

func saveConfig() error {
	configData, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		return fmt.Errorf("error marshaling config: %w", err)
	}

	if err := os.WriteFile("config.json", configData, 0644); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	return nil
}

func main() {
	fmt.Println("Starting Discord selfbot...")
	fmt.Printf("Using token: %s...\n", config.Token[:15])
	fmt.Printf("Owner ID: %.0f\n", config.OwnerID)
	fmt.Printf("Command prefix: %s\n", config.Prefix)

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

	fmt.Println("Shutting down...")
}
