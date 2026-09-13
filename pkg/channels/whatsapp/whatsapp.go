package whatsapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/channels"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/identity"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/utils"
)

type WhatsAppChannel struct {
	*channels.BaseChannel
	conn      *websocket.Conn
	config    *config.WhatsAppSettings
	url       string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	connected bool
}

func NewWhatsAppChannel(
	bc *config.Channel,
	cfg *config.WhatsAppSettings,
	bus *bus.MessageBus,
) (*WhatsAppChannel, error) {
	base := channels.NewBaseChannel(
		"whatsapp",
		cfg,
		bus,
		bc.AllowFrom,
		channels.WithMaxMessageLength(65536),
		channels.WithReasoningChannelID(bc.ReasoningChannelID),
	)

	return &WhatsAppChannel{
		BaseChannel: base,
		config:      cfg,
		url:         cfg.BridgeURL,
		connected:   false,
	}, nil
}

func (c *WhatsAppChannel) Start(ctx context.Context) error {
	logger.InfoCF("whatsapp", "Starting WhatsApp channel", map[string]any{
		"bridge_url": c.url,
	})

	c.ctx, c.cancel = context.WithCancel(ctx)

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = config.Global().ChannelConnectTimeout()

	conn, resp, err := dialer.Dial(c.url, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		c.cancel()
		return fmt.Errorf("failed to connect to WhatsApp bridge: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	c.SetRunning(true)
	logger.InfoC("whatsapp", "WhatsApp channel connected")

	go c.listen()

	return nil
}

func (c *WhatsAppChannel) Stop(ctx context.Context) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp channel...")

	// Cancel context first to signal listen goroutine to exit
	if c.cancel != nil {
		c.cancel()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			logger.ErrorCF("whatsapp", "Error closing WhatsApp connection", map[string]any{
				"error": err.Error(),
			})
		}
		c.conn = nil
	}

	c.connected = false
	c.SetRunning(false)

	return nil
}

func (c *WhatsAppChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}

	// Check ctx before acquiring lock
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("whatsapp connection not established: %w", channels.ErrTemporary)
	}

	payload := map[string]any{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(config.Global().ChannelCommandTimeout()))
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		_ = c.conn.SetWriteDeadline(time.Time{})
		return nil, fmt.Errorf("whatsapp send: %w", channels.ErrTemporary)
	}
	_ = c.conn.SetWriteDeadline(time.Time{})

	return nil, nil
}

// SendMedia implements channels.MediaSender. The bridge protocol carries each
// attachment as a JSON object with the file payload base64-encoded, mirroring
// how bridges such as whatsapp-web.js wrappers accept outbound media.
func (c *WhatsAppChannel) SendMedia(
	ctx context.Context,
	msg bus.OutboundMediaMessage,
) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	store := c.GetMediaStore()
	if store == nil {
		return nil, fmt.Errorf("no media store available: %w", channels.ErrSendFailed)
	}

	var messageIDs []string
	for _, part := range msg.Parts {
		localPath, err := store.Resolve(part.Ref)
		if err != nil {
			logger.ErrorCF("whatsapp", "Failed to resolve media ref", map[string]any{
				"ref":   part.Ref,
				"error": err.Error(),
			})
			continue
		}
		data, err := os.ReadFile(localPath)
		if err != nil {
			logger.ErrorCF("whatsapp", "Failed to read media file", map[string]any{
				"path":  localPath,
				"error": err.Error(),
			})
			continue
		}

		filename := part.Filename
		if filename == "" {
			filename = filepath.Base(localPath)
		}
		mime := part.ContentType
		if mime == "" {
			mime = http.DetectContentType(data)
		}

		payload := map[string]any{
			"type":       "media",
			"to":         msg.ChatID,
			"media_type": part.Type,
			"data":       base64.StdEncoding.EncodeToString(data),
			"filename":   filename,
			"mime_type":  mime,
			"caption":    part.Caption,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return messageIDs, fmt.Errorf("failed to marshal media message: %w", err)
		}

		c.mu.Lock()
		conn := c.conn
		if conn != nil {
			_ = conn.SetWriteDeadline(time.Now().Add(config.Global().ChannelCommandTimeout()))
			err = conn.WriteMessage(websocket.TextMessage, encoded)
			_ = conn.SetWriteDeadline(time.Time{})
		}
		c.mu.Unlock()

		if conn == nil {
			return messageIDs, fmt.Errorf("whatsapp connection not established: %w", channels.ErrTemporary)
		}
		if err != nil {
			return messageIDs, fmt.Errorf("whatsapp send media: %w", channels.ErrTemporary)
		}
	}

	return messageIDs, nil
}

func (c *WhatsAppChannel) listen() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()

			if conn == nil {
				time.Sleep(config.Global().ChannelReconnectInitial())
				continue
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				logger.ErrorCF("whatsapp", "WhatsApp read error", map[string]any{
					"error": err.Error(),
				})
				time.Sleep(config.Global().ChannelReconnectInitial())
				continue
			}

			var msg map[string]any
			if err := json.Unmarshal(message, &msg); err != nil {
				logger.ErrorCF("whatsapp", "Failed to unmarshal WhatsApp message", map[string]any{
					"error": err.Error(),
				})
				continue
			}

			msgType, ok := msg["type"].(string)
			if !ok {
				continue
			}

			if msgType == "message" {
				c.handleIncomingMessage(msg)
			}
		}
	}
}

func (c *WhatsAppChannel) handleIncomingMessage(msg map[string]any) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	content, ok := msg["content"].(string)
	if !ok {
		content = ""
	}

	var mediaPaths []string
	if mediaData, ok := msg["media"].([]any); ok {
		mediaPaths = make([]string, 0, len(mediaData))
		for _, m := range mediaData {
			if path, ok := m.(string); ok {
				mediaPaths = append(mediaPaths, path)
			}
		}
	}

	metadata := make(map[string]string)
	var messageID string
	if mid, ok := msg["id"].(string); ok {
		messageID = mid
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	logger.InfoCF("whatsapp", "WhatsApp message received", map[string]any{
		"sender":  senderID,
		"preview": utils.Truncate(content, 50),
	})

	sender := bus.SenderInfo{
		Platform:    "whatsapp",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("whatsapp", senderID),
	}
	if display, ok := metadata["user_name"]; ok {
		sender.DisplayName = display
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	inboundCtx := bus.InboundContext{
		Channel:   "whatsapp",
		ChatID:    chatID,
		SenderID:  senderID,
		MessageID: messageID,
		Raw:       metadata,
	}
	if chatID == senderID {
		inboundCtx.ChatType = "direct"
	} else {
		inboundCtx.ChatType = "group"
	}

	c.HandleInboundContext(c.ctx, chatID, content, mediaPaths, inboundCtx, sender)
}
