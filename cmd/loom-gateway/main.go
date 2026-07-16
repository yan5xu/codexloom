// loom-gateway runs external platform adapters outside the CodexLoom process.
// The fake provider is a durable protocol probe used by integration tests and
// local development; real providers use the same ingress/command/result API.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	hub        string
	connection string
	provider   string
	token      string
	output     string
	ackDelay   time.Duration
}

type eventEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type connectorCommand struct {
	Type       string `json:"type"`
	Connection struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	} `json:"connection"`
	Address struct {
		ID string `json:"id"`
	} `json:"address"`
	OutboxItem struct {
		ID             string `json:"id"`
		IdempotencyKey string `json:"idempotencyKey"`
		AttemptToken   string `json:"attemptToken"`
		Content        struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"outboxItem"`
}

func main() {
	cfg := config{}
	serviceURL := envOr("CODEX_LOOM_URL", envOr("CHUB_URL", "http://127.0.0.1:4870"))
	flag.StringVar(&cfg.hub, "service", serviceURL, "CodexLoom URL")
	flag.StringVar(&cfg.hub, "hub", serviceURL, "deprecated alias for -service")
	flag.StringVar(&cfg.connection, "connection", "", "platform connection id")
	flag.StringVar(&cfg.provider, "provider", "fake", "adapter provider")
	connectorToken := envOr("CODEX_LOOM_CONNECTOR_TOKEN", os.Getenv("CODEX_HUB_CONNECTOR_TOKEN"))
	flag.StringVar(&cfg.token, "token", connectorToken, "connector API token")
	flag.StringVar(&cfg.output, "output", "", "fake provider delivery ledger")
	flag.DurationVar(&cfg.ackDelay, "ack-delay", 0, "fake provider delay between delivery and result ack")
	flag.Parse()
	cfg.hub = strings.TrimRight(cfg.hub, "/")
	if cfg.connection == "" {
		log.Fatal("-connection is required")
	}
	if cfg.provider != "fake" {
		log.Fatalf("provider %q is not built into this gateway", cfg.provider)
	}
	if cfg.output == "" {
		cfg.output = filepath.Join(os.TempDir(), "loom-gateway-"+cfg.connection+".ndjson")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go heartbeatLoop(ctx, cfg)
	for ctx.Err() == nil {
		if err := consumeCommands(ctx, cfg); err != nil && ctx.Err() == nil {
			log.Printf("command stream: %v; reconnecting", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func heartbeatLoop(ctx context.Context, cfg config) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		_ = postJSON(ctx, cfg, "/api/integrations/connections/"+cfg.connection+"/heartbeat", map[string]any{
			"status": "connected", "capabilities": []string{"receive_events", "proactive_send", "threads"},
		}, nil)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func consumeCommands(ctx context.Context, cfg config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.hub+"/api/integrations/connections/"+cfg.connection+"/commands", nil)
	if err != nil {
		return err
	}
	setConnectorToken(req, cfg.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("commands HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var envelope eventEnvelope
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope) != nil || envelope.Type != "connector/command" {
			continue
		}
		var command connectorCommand
		if err := json.Unmarshal(envelope.Data, &command); err != nil {
			log.Printf("decode command: %v", err)
			continue
		}
		if err := deliverFake(cfg.output, command); err != nil {
			_ = reportResult(ctx, cfg, command.OutboxItem.ID, command.OutboxItem.AttemptToken, false, "", err.Error())
			continue
		}
		if cfg.ackDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.ackDelay):
			}
		}
		externalID := "fake_" + strings.TrimPrefix(command.OutboxItem.ID, "out_")
		if err := reportResult(ctx, cfg, command.OutboxItem.ID, command.OutboxItem.AttemptToken, true, externalID, ""); err != nil {
			return err
		}
		log.Printf("delivered %s as %s", command.OutboxItem.ID, externalID)
	}
	return scanner.Err()
}

func deliverFake(path string, command connectorCommand) error {
	if fakeAlreadyDelivered(path, command.OutboxItem.IdempotencyKey) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	record := map[string]any{
		"deliveredAt": time.Now().UTC().Format(time.RFC3339Nano),
		"command":     command,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func fakeAlreadyDelivered(path, idempotencyKey string) bool {
	if idempotencyKey == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record struct {
			Command connectorCommand `json:"command"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Command.OutboxItem.IdempotencyKey == idempotencyKey {
			return true
		}
	}
	return false
}

func reportResult(ctx context.Context, cfg config, outboxID, attemptToken string, success bool, externalID, message string) error {
	return postJSON(ctx, cfg,
		"/api/integrations/connections/"+cfg.connection+"/outbox/"+outboxID+"/result",
		map[string]any{"attemptToken": attemptToken, "success": success, "externalMessageId": externalID, "error": message}, nil)
}

func postJSON(ctx context.Context, cfg config, path string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.hub+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setConnectorToken(req, cfg.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func setConnectorToken(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("X-Codex-Loom-Connector-Token", token)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
