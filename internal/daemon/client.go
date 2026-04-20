package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Client is a one-shot connection to a daemon socket.
type Client struct {
	Socket  string
	Timeout time.Duration
}

// NewClient returns a Client for the socket. Timeout defaults to 2s.
func NewClient(socket string) *Client {
	return &Client{Socket: socket, Timeout: 2 * time.Second}
}

// Call sends one request and returns the response.
func (c *Client) Call(req Request) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.Socket, c.Timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if c.Timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}
