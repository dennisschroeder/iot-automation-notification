package nats

import (
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

type Client struct {
	nc *nats.Conn
	js nats.JetStreamContext
	kv nats.KeyValue
}

func NewClient(url string) (*Client, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize JetStream: %w", err)
	}
	
	// Ensure we can access the state bucket (used for automation locks)
	kv, err := js.KeyValue("iot_state")
	if err != nil {
		slog.Warn("Failed to bind to KeyValue bucket 'iot_state' (it might not exist yet)", "error", err)
	}

	slog.Info("NATS JetStream client initialized", "url", url)
	return &Client{nc: nc, js: js, kv: kv}, nil
}

func (c *Client) Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	return c.nc.Subscribe(subject, handler)
}

func (c *Client) Publish(subject string, data []byte) error {
	return c.nc.Publish(subject, data)
}

func (c *Client) GetKV(key string) (string, error) {
	if c.kv == nil {
		return "", fmt.Errorf("KV store not initialized")
	}
	entry, err := c.kv.Get(key)
	if err != nil {
		return "", err
	}
	return string(entry.Value()), nil
}

func (c *Client) PutKV(key string, data []byte) error {
	if c.kv == nil {
		return fmt.Errorf("KV store not initialized")
	}
	_, err := c.kv.Put(key, data)
	return err
}

func (c *Client) JetStream() nats.JetStreamContext {
	return c.js
}

func (c *Client) Close() {
	c.nc.Close()
}
