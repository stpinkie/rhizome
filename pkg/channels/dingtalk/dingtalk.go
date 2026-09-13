// Rhizome - Ultra-lightweight personal AI agent
// DingTalk channel implementation using Stream Mode

package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	dinglog "github.com/open-dingtalk/dingtalk-stream-sdk-go/logger"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/channels"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/identity"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/utils"
)

// DingTalkChannel implements the Channel interface for DingTalk (钉钉)
// It uses WebSocket for receiving messages via stream mode and API for sending
type DingTalkChannel struct {
	*channels.BaseChannel
	config       *config.DingTalkSettings
	clientID     string
	clientSecret string
	streamClient *client.StreamClient
	ctx          context.Context
	cancel       context.CancelFunc
	// Map to store session webhooks for each chat
	sessionWebhooks sync.Map // chatID -> sessionWebhook

	tokenMu      sync.Mutex
	cachedToken  string
	tokenExpiry  time.Time
	mediaHTTPCli *http.Client
}

// NewDingTalkChannel creates a new DingTalk channel instance
func NewDingTalkChannel(
	bc *config.Channel,
	cfg *config.DingTalkSettings,
	messageBus *bus.MessageBus,
) (*DingTalkChannel, error) {
	if cfg.ClientID == "" || cfg.ClientSecret.String() == "" {
		return nil, fmt.Errorf("dingtalk client_id and client_secret are required")
	}

	// Set the logger for the Stream SDK
	dinglog.SetLogger(logger.NewLogger("dingtalk"))

	base := channels.NewBaseChannel("dingtalk", cfg, messageBus, bc.AllowFrom,
		channels.WithMaxMessageLength(20000),
		channels.WithGroupTrigger(bc.GroupTrigger),
		channels.WithReasoningChannelID(bc.ReasoningChannelID),
	)

	return &DingTalkChannel{
		BaseChannel:  base,
		config:       cfg,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret.String(),
	}, nil
}

// Start initializes the DingTalk channel with Stream Mode
func (c *DingTalkChannel) Start(ctx context.Context) error {
	logger.InfoC("dingtalk", "Starting DingTalk channel (Stream Mode)...")

	c.ctx, c.cancel = context.WithCancel(ctx)

	// Create credential config
	cred := client.NewAppCredentialConfig(c.clientID, c.clientSecret)

	// Create the stream client with options
	c.streamClient = client.NewStreamClient(
		client.WithAppCredential(cred),
		client.WithAutoReconnect(true),
	)

	// Register chatbot callback handler (IChatBotMessageHandler is a function type)
	c.streamClient.RegisterChatBotCallbackRouter(c.onChatBotMessageReceived)

	// Start the stream client
	if err := c.streamClient.Start(c.ctx); err != nil {
		return fmt.Errorf("failed to start stream client: %w", err)
	}

	c.SetRunning(true)
	logger.InfoC("dingtalk", "DingTalk channel started (Stream Mode)")
	return nil
}

// Stop gracefully stops the DingTalk channel
func (c *DingTalkChannel) Stop(ctx context.Context) error {
	logger.InfoC("dingtalk", "Stopping DingTalk channel...")

	if c.cancel != nil {
		c.cancel()
	}

	if c.streamClient != nil {
		c.streamClient.Close()
	}

	c.SetRunning(false)
	logger.InfoC("dingtalk", "DingTalk channel stopped")
	return nil
}

// Send sends a message to DingTalk via the chatbot reply API
func (c *DingTalkChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}

	// Get session webhook from storage
	sessionWebhookRaw, ok := c.sessionWebhooks.Load(msg.ChatID)
	if !ok {
		return nil, fmt.Errorf("no session_webhook found for chat %s, cannot send message", msg.ChatID)
	}

	sessionWebhook, ok := sessionWebhookRaw.(string)
	if !ok {
		return nil, fmt.Errorf("invalid session_webhook type for chat %s", msg.ChatID)
	}

	logger.DebugCF("dingtalk", "Sending message", map[string]any{
		"chat_id": msg.ChatID,
		"preview": utils.Truncate(msg.Content, 100),
	})

	// Use the session webhook to send the reply
	return nil, c.SendDirectReply(ctx, sessionWebhook, msg.Content)
}

// onChatBotMessageReceived implements the IChatBotMessageHandler function signature
// This is called by the Stream SDK when a new message arrives
// IChatBotMessageHandler is: func(c context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error)
func (c *DingTalkChannel) onChatBotMessageReceived(
	ctx context.Context,
	data *chatbot.BotCallbackDataModel,
) ([]byte, error) {
	if data == nil {
		return nil, nil
	}

	// Extract message content from Text field
	content := strings.TrimSpace(data.Text.Content)
	if content == "" {
		// Try to extract from Content interface{} if Text is empty
		if contentMap, ok := data.Content.(map[string]any); ok {
			if textContent, ok := contentMap["content"].(string); ok {
				content = strings.TrimSpace(textContent)
			}
		}
	}

	if content == "" && dingTalkDownloadCode(data) == "" {
		return nil, nil // Ignore empty messages
	}

	senderID := strings.TrimSpace(data.SenderStaffId)
	if senderID == "" {
		senderID = strings.TrimSpace(data.SenderId)
	}
	senderNick := strings.TrimSpace(data.SenderNick)

	chatID := strings.TrimSpace(data.ConversationId)
	if chatID == "" && data.ConversationType == "1" {
		// Fallback for direct chats when conversation_id is absent.
		chatID = senderID
	}
	if chatID == "" {
		return nil, nil
	}

	// Store the session webhook for this chat so we can reply later
	c.sessionWebhooks.Store(chatID, data.SessionWebhook)

	metadata := map[string]string{
		"sender_name":       senderNick,
		"conversation_id":   data.ConversationId,
		"conversation_type": data.ConversationType,
		"platform":          "dingtalk",
		"session_webhook":   data.SessionWebhook,
	}

	var (
		chatType    string
		isMentioned bool
	)
	if data.ConversationType == "1" {
		chatType = "direct"
	} else {
		chatType = "group"
		isMentioned = data.IsInAtList
		if isMentioned {
			content = stripLeadingAtMentions(content)
		}
		// In group chats, apply unified group trigger filtering
		respond, cleaned := c.ShouldRespondInGroup(isMentioned, content)
		if !respond {
			return nil, nil
		}
		content = cleaned
	}

	// Attachment messages (picture/audio/video/file/richText) carry a
	// downloadCode instead of text — fetch them into the media store.
	var mediaRefs []string
	if code := dingTalkDownloadCode(data); code != "" {
		if ref := c.downloadRobotFile(ctx, code, chatID, data.MsgId); ref != "" {
			mediaRefs = append(mediaRefs, ref)
		}
		if content == "" {
			content = fmt.Sprintf("[%s]", data.Msgtype)
		}
	}

	logger.DebugCF("dingtalk", "Received message", map[string]any{
		"sender_nick": senderNick,
		"sender_id":   senderID,
		"preview":     utils.Truncate(content, 50),
	})

	// Build sender info
	platformID := senderID
	if platformID == "" {
		platformID = chatID
	}
	resolvedSenderID := senderID
	if resolvedSenderID == "" {
		resolvedSenderID = platformID
	}
	sender := bus.SenderInfo{
		Platform:    "dingtalk",
		PlatformID:  platformID,
		CanonicalID: identity.BuildCanonicalID("dingtalk", platformID),
		DisplayName: senderNick,
	}

	if !c.IsAllowedSender(sender) {
		return nil, nil
	}

	inboundCtx := bus.InboundContext{
		Channel:   "dingtalk",
		ChatID:    chatID,
		ChatType:  chatType,
		SenderID:  resolvedSenderID,
		Mentioned: isMentioned,
		Raw:       metadata,
	}
	if data.SessionWebhook != "" {
		inboundCtx.ReplyHandles = map[string]string{
			"session_webhook": data.SessionWebhook,
		}
	}

	c.HandleInboundContext(ctx, chatID, content, mediaRefs, inboundCtx, sender)

	// Return nil to indicate we've handled the message asynchronously
	// The response will be sent through the message bus
	return nil, nil
}

// SendDirectReply sends a direct reply using the session webhook
func (c *DingTalkChannel) SendDirectReply(ctx context.Context, sessionWebhook, content string) error {
	replier := chatbot.NewChatbotReplier()

	// Convert string content to []byte for the API
	contentBytes := []byte(content)
	titleBytes := []byte("Rhizome")

	// Send markdown formatted reply
	err := replier.SimpleReplyMarkdown(
		ctx,
		sessionWebhook,
		titleBytes,
		contentBytes,
	)
	if err != nil {
		return fmt.Errorf("dingtalk send: %w", channels.ErrTemporary)
	}

	return nil
}

// SendMedia implements channels.MediaSender. Each part is uploaded through
// the legacy oapi media endpoint and delivered through the session webhook as
// an image or file reply. When upload fails (e.g. the app credential is a
// new-style suite key that oapi does not accept), the part degrades to a
// markdown text reply carrying the caption and filename.
func (c *DingTalkChannel) SendMedia(
	ctx context.Context,
	msg bus.OutboundMediaMessage,
) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}

	sessionWebhookRaw, ok := c.sessionWebhooks.Load(msg.ChatID)
	if !ok {
		return nil, fmt.Errorf("no session_webhook found for chat %s, cannot send media", msg.ChatID)
	}
	sessionWebhook, ok := sessionWebhookRaw.(string)
	if !ok {
		return nil, fmt.Errorf("invalid session_webhook type for chat %s", msg.ChatID)
	}

	store := c.GetMediaStore()
	if store == nil {
		return nil, fmt.Errorf("no media store available: %w", channels.ErrSendFailed)
	}

	replier := chatbot.NewChatbotReplier()
	for _, part := range msg.Parts {
		localPath, err := store.Resolve(part.Ref)
		if err != nil {
			logger.ErrorCF("dingtalk", "Failed to resolve media ref", map[string]any{
				"ref":   part.Ref,
				"error": err.Error(),
			})
			continue
		}
		data, err := os.ReadFile(localPath)
		if err != nil {
			logger.ErrorCF("dingtalk", "Failed to read media file", map[string]any{
				"path":  localPath,
				"error": err.Error(),
			})
			continue
		}

		filename := part.Filename
		if filename == "" {
			filename = filepath.Base(localPath)
		}
		uploadType := dingTalkUploadType(part.Type)

		mediaID, err := c.uploadMedia(ctx, uploadType, filename, data)
		if err != nil {
			logger.ErrorCF("dingtalk", "Media upload failed, falling back to text", map[string]any{
				"type":  part.Type,
				"error": err.Error(),
			})
			fallback := part.Caption
			if fallback != "" {
				fallback += "\n\n"
			}
			fallback += fmt.Sprintf("[attachment: %s]", filename)
			if rerr := replier.SimpleReplyMarkdown(
				ctx, sessionWebhook, []byte("Rhizome"), []byte(fallback),
			); rerr != nil {
				return nil, fmt.Errorf("dingtalk media fallback reply: %w", channels.ErrTemporary)
			}
			continue
		}

		body := dingTalkMediaReplyBody(part.Type, mediaID)
		if part.Caption != "" {
			if rerr := replier.SimpleReplyMarkdown(
				ctx, sessionWebhook, []byte("Rhizome"), []byte(part.Caption),
			); rerr != nil {
				return nil, fmt.Errorf("dingtalk caption reply: %w", channels.ErrTemporary)
			}
		}
		if err := replier.ReplyMessage(ctx, sessionWebhook, body); err != nil {
			return nil, fmt.Errorf("dingtalk media reply: %w", channels.ErrTemporary)
		}
	}

	return nil, nil
}

// dingTalkUploadType maps a MediaPart type to the oapi media/upload "type".
func dingTalkUploadType(partType string) string {
	switch partType {
	case "image":
		return "image"
	case "audio":
		return "voice"
	default:
		return "file"
	}
}

// dingTalkMediaReplyBody builds the session-webhook reply payload. Session
// webhooks only support image and file msgtypes, so audio/video parts sent
// through this path go out as file attachments.
func dingTalkMediaReplyBody(partType, mediaID string) map[string]interface{} {
	if partType == "image" {
		return map[string]interface{}{
			"msgtype": "image",
			"image":   map[string]string{"media_id": mediaID},
		}
	}
	return map[string]interface{}{
		"msgtype": "file",
		"file":    map[string]string{"media_id": mediaID},
	}
}

const (
	dingTalkTokenURL       = "https://oapi.dingtalk.com/gettoken"
	dingTalkMediaUploadURL = "https://oapi.dingtalk.com/media/upload"
)

// accessToken returns a cached DingTalk access token, refreshing it via the
// oapi gettoken endpoint when expired. The stream-mode client_id/client_secret
// double as the appkey/appsecret for this endpoint.
func (c *DingTalkChannel) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry.Add(-time.Minute)) {
		return c.cachedToken, nil
	}

	reqURL := fmt.Sprintf("%s?appkey=%s&appsecret=%s",
		dingTalkTokenURL,
		url.QueryEscape(c.clientID),
		url.QueryEscape(c.clientSecret),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.mediaHTTP().Do(req)
	if err != nil {
		return "", fmt.Errorf("gettoken request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var parsed struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("gettoken response: %w", err)
	}
	if parsed.ErrCode != 0 || parsed.AccessToken == "" {
		return "", fmt.Errorf("gettoken failed: errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}

	c.cachedToken = parsed.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	return c.cachedToken, nil
}

// uploadMedia posts the file to oapi media/upload and returns the media_id.
func (c *DingTalkChannel) uploadMedia(
	ctx context.Context,
	uploadType, filename string,
	data []byte,
) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("media", filename)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	reqURL := fmt.Sprintf("%s?access_token=%s&type=%s",
		dingTalkMediaUploadURL,
		url.QueryEscape(token),
		url.QueryEscape(uploadType),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.mediaHTTP().Do(req)
	if err != nil {
		return "", fmt.Errorf("media upload request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var parsed struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("media upload response: %w", err)
	}
	if parsed.ErrCode != 0 || parsed.MediaID == "" {
		return "", fmt.Errorf("media upload failed: errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	return parsed.MediaID, nil
}

// dingTalkDownloadCode extracts the inbound attachment downloadCode for
// non-text msgtypes (picture/audio/video/file/richText). Returns "" for text
// messages or payloads without a code.
func dingTalkDownloadCode(data *chatbot.BotCallbackDataModel) string {
	switch data.Msgtype {
	case "picture", "audio", "video", "file", "richText":
	default:
		return ""
	}
	contentMap, ok := data.Content.(map[string]any)
	if !ok {
		return ""
	}
	// Most types nest the code under "content"; richText keeps the same shape.
	if inner, ok := contentMap["content"].(map[string]any); ok {
		contentMap = inner
	}
	if code, ok := contentMap["downloadCode"].(string); ok {
		return code
	}
	return ""
}

const dingTalkFileDownloadURL = "https://oapi.dingtalk.com/robot/messageFiles/download"

// downloadRobotFile exchanges an inbound downloadCode for a download URL,
// fetches the file into the media temp dir, and registers it with the media
// store. Returns the media ref (or raw path with no store), "" on failure.
func (c *DingTalkChannel) downloadRobotFile(
	ctx context.Context,
	downloadCode, chatID, messageID string,
) string {
	token, err := c.accessToken(ctx)
	if err != nil {
		logger.ErrorCF("dingtalk", "Failed to get access token for media download", map[string]any{
			"error": err.Error(),
		})
		return ""
	}

	reqBody, _ := json.Marshal(map[string]string{
		"downloadCode": downloadCode,
		"robotCode":    c.clientID,
	})
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		dingTalkFileDownloadURL+"?access_token="+url.QueryEscape(token),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.mediaHTTP().Do(req)
	if err != nil {
		logger.ErrorCF("dingtalk", "Media download request failed", map[string]any{
			"error": err.Error(),
		})
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var parsed struct {
		DownloadURL string `json:"downloadUrl"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.DownloadURL == "" {
		logger.ErrorCF("dingtalk", "Media download returned no URL", map[string]any{
			"body": utils.Truncate(string(body), 200),
		})
		return ""
	}

	localPath := utils.DownloadFile(parsed.DownloadURL, "attachment", utils.DownloadOptions{
		LoggerPrefix: "dingtalk",
	})
	if localPath == "" {
		return ""
	}

	if store := c.GetMediaStore(); store != nil {
		scope := channels.BuildMediaScope("dingtalk", chatID, messageID)
		ref, err := store.Store(localPath, media.MediaMeta{
			Filename:      filepath.Base(localPath),
			Source:        "dingtalk",
			CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
		}, scope)
		if err == nil {
			return ref
		}
	}
	return localPath
}

func (c *DingTalkChannel) mediaHTTP() *http.Client {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.mediaHTTPCli == nil {
		c.mediaHTTPCli = &http.Client{Timeout: config.Global().ChannelMediaTimeout()}
	}
	return c.mediaHTTPCli
}

func stripLeadingAtMentions(content string) string {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return ""
	}

	i := 0
	for i < len(fields) && strings.HasPrefix(fields[i], "@") {
		i++
	}
	if i == 0 {
		return strings.TrimSpace(content)
	}
	return strings.Join(fields[i:], " ")
}
