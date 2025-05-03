package discordrpc

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rikkuness/discord-rpc/ipc"
)

// Client wrapper for the Discord RPC client
type Client struct {
	ClientID string
	Socket   *ipc.Socket
}

// Data struct for RPC responses
type Data struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// handshake struct for RPC handshake
type handshake struct {
	Version   string `json:"v"`
	ClientID  string `json:"client_id"`
}

// RPCCommand struct for RPC commands
type RPCCommand struct {
	Command   string      `json:"cmd"`
	Arguments interface{} `json:"args"`
	Nonce     string      `json:"nonce,omitempty"`
}

// Activity represents a Discord Rich Presence activity
type Activity struct {
	State      string `json:"state,omitempty"`
	Details    string `json:"details,omitempty"`
	StartTimestamp int64 `json:"start_timestamp,omitempty"`
	EndTimestamp   int64 `json:"end_timestamp,omitempty"`
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
	SmallImage string `json:"small_image,omitempty"`
	SmallText  string `json:"small_text,omitempty"`
}

// ActivityArgs represents the arguments for the SET_ACTIVITY command
type ActivityArgs struct {
	Pid      int      `json:"pid"`
	Activity Activity `json:"activity"`
}

// New sends a handshake in the socket and returns an error or nil and an instance of Client
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

// SetActivity sets the Discord Rich Presence activity
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

	_, err = c.Socket.Send(1, string(jsonPayload))
	return err
}

// Close closes the RPC connection
func (c *Client) Close() error {
	if c.Socket != nil {
		return c.Socket.Close()
	}
	return nil
}