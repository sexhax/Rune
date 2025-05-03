package ipc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
)

// GetIpcPath chooses the correct directory to the ipc socket and returns it
func GetIpcPath() string {
	variablesnames := []string{"XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"}

	for _, variablename := range variablesnames {
		path, exists := os.LookupEnv(variablename)

		if exists {
			return path
		}
	}

	return "/tmp"
}

// Socket extends net.Conn methods
type Socket struct {
	net.Conn
}

// Read the socket response
func (socket *Socket) Read() (string, error) {
	buf := make([]byte, 512)
	payloadlength, err := socket.Conn.Read(buf)
	if err != nil {
		return "", err
	}

	buffer := new(bytes.Buffer)
	for i := 8; i < payloadlength; i++ {
		buffer.WriteByte(buf[i])
	}

	r := buffer.String()
	if r == "" {
		return "", fmt.Errorf("Empty response")
	}

	return r, nil
}

// Send opcode and payload to the unix socket
func (socket *Socket) Send(opcode int, payload string) (string, error) {
	buf := new(bytes.Buffer)

	fmt.Println(payload)
	err := binary.Write(buf, binary.LittleEndian, int32(opcode))
	if err != nil {
		return "", err
	}

	err = binary.Write(buf, binary.LittleEndian, int32(len(payload)))
	if err != nil {
		return "", err
	}

	buf.Write([]byte(payload))
	_, err = socket.Write(buf.Bytes())
	if err != nil {
		return "", err
	}

	return socket.Read()
}
