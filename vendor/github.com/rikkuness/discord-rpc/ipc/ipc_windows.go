// +build windows

package ipc

import (
	"time"

	npipe "gopkg.in/natefinch/npipe.v2"
)

// NewConnection opens the discord-ipc-0 named pipe
func NewConnection() (*Socket, error) {
	// Connect to the Windows named pipe, this is a well known name
	// We use DialTimeout since it will block forever (or very very long) on Windows
	// if the pipe is not available (Discord not running)
	sock, err := npipe.DialTimeout(`\\.\pipe\discord-ipc-0`, time.Second*2)
	if err != nil {
		return nil, err
	}

	return &Socket{sock}, nil
}
