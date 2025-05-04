package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/rikkuness/discord-rpc/ipc"
)

type Config struct {
	Token   string `json:"token"`
	OwnerID int64  `json:"owner_id"`
	Prefix  string `json:"prefix"`
	RPC     struct {
		Enabled     bool   `json:"enabled"`
		ApplicationID string `json:"application_id"`
		State       string `json:"state"`
		Details     string `json:"details"`
		LargeImage  string `json:"large_image"`
		LargeText   string `json:"large_text"`
	} `json:"rpc"`
}

type Client struct {
	ClientID string
	Socket   *ipc.Socket
}

type Data struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type handshake struct {
	Version   string `json:"v"`
	ClientID  string `json:"client_id"`
}

type Activity struct {
	State          string `json:"state,omitempty"`
	Details        string `json:"details,omitempty"`
	StartTimestamp int64  `json:"start_timestamp,omitempty"`
	EndTimestamp   int64  `json:"end_timestamp,omitempty"`
	LargeImage     string `json:"large_image,omitempty"`
	LargeText      string `json:"large_text,omitempty"`
	SmallImage     string `json:"small_image,omitempty"`
	SmallText      string `json:"small_text,omitempty"`
}

type ActivityArgs struct {
	Pid      int      `json:"pid"`
	Activity Activity `json:"activity"`
}

type RPCCommand struct {
	Command   string      `json:"cmd"`
	Arguments interface{} `json:"args"`
	Nonce     string      `json:"nonce,omitempty"`
}

func New(clientid string) (*Client, error) {
	if clientid == "" {
		return nil, fmt.Errorf("no clientid set")
	}

	payload, err := json.Marshal(handshake{"1", clientid})
	if err != nil {
		return nil, err
	}

	sock, err := ipc.NewConnection()
	if err != nil {
		return nil, err
	}

	c := &Client{Socket: sock, ClientID: clientid}

	r, err := c.Socket.Send(0, string(payload))
	if err != nil {
		return nil, err
	}

	var responseBody Data
	if err := json.Unmarshal([]byte(r), &responseBody); err != nil {
		return nil, err
	}

	if responseBody.Code > 1000 {
		return nil, fmt.Errorf(responseBody.Message)
	}

	return c, nil
}

func (c *Client) SetActivity(state, details, largeImage, largeText string) error {
	pid := os.Getpid()

	activity := Activity{
		State:      state,
		Details:    details,
		LargeImage: largeImage,
		LargeText:  largeText,
	}

	args := ActivityArgs{
		Pid:      pid,
		Activity: activity,
	}

	payload := RPCCommand{
		Command:   "SET_ACTIVITY",
		Arguments: args,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	response, err := c.Socket.Send(1, string(jsonPayload))
	if err != nil {
		return err
	}

	var responseBody Data
	if err := json.Unmarshal([]byte(response), &responseBody); err != nil {
		return err
	}

	if responseBody.Code != 200 {
		return fmt.Errorf("unexpected response code: %d, message: %s", responseBody.Code, responseBody.Message)
	}

	return nil
}

func (c *Client) Close() error {
	if c.Socket != nil {
		return c.Socket.Close()
	}
	return nil
}

func loadConfig() (Config, error) {
	var config Config

	configFile := "config.json" // File path of your config.json
	configData, err := os.ReadFile(configFile)
	if err != nil {
		return config, fmt.Errorf("Error reading config file: %w", err)
	}

	err = json.Unmarshal(configData, &config)
	if err != nil {
		return config, fmt.Errorf("Error parsing config file: %w", err)
	}

	return config, nil
}

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file")
	}

	// Example usage
	config, err := loadConfig()
	if err != nil {
		fmt.Println("Error loading config:", err)
		return
	}

	client, err := New(config.RPC.ApplicationID)
	if err != nil {
		fmt.Println("Error creating RPC client:", err)
		return
	}
	defer client.Close()

	err = client.SetActivity("Playing a game", "In a match", "large_image_key", "Large Image Text")
	if err != nil {
		fmt.Println("Error setting activity:", err)
		return
	}

	fmt.Println("Activity set successfully")

	// Keep the program running
	for {
		time.Sleep(1 * time.Minute)
	}
}
