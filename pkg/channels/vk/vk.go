package vk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SevereCloud/vksdk/v3/api"
	"github.com/SevereCloud/vksdk/v3/api/params"
	"github.com/SevereCloud/vksdk/v3/events"
	"github.com/SevereCloud/vksdk/v3/longpoll-bot"
	"github.com/SevereCloud/vksdk/v3/object"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/channels"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/identity"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/utils"
)

type VKChannel struct {
	*channels.BaseChannel
	vk          *api.VK
	lp          *longpoll.LongPoll
	channelName string
	bc          *config.Channel
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewVKChannel(channelName string, bc *config.Channel, bus *bus.MessageBus) (*VKChannel, error) {
	var vkCfg config.VKSettings
	if err := bc.Decode(&vkCfg); err != nil {
		return nil, err
	}

	vk := api.NewVK(vkCfg.Token.String())

	base := channels.NewBaseChannel(
		channelName,
		&vkCfg,
		bus,
		bc.AllowFrom,
		channels.WithMaxMessageLength(4000),
		channels.WithGroupTrigger(bc.GroupTrigger),
		channels.WithReasoningChannelID(bc.ReasoningChannelID),
	)

	return &VKChannel{
		BaseChannel: base,
		vk:          vk,
		channelName: channelName,
		bc:          bc,
	}, nil
}

func (c *VKChannel) getVKCfg() *config.VKSettings {
	var v config.VKSettings
	if err := c.bc.Decode(&v); err != nil {
		return nil
	}
	return &v
}

func (c *VKChannel) Start(ctx context.Context) error {
	logger.InfoC("vk", "Starting VK bot (Long Poll mode)...")

	c.ctx, c.cancel = context.WithCancel(ctx)

	groupID := c.getVKCfg().GroupID
	if groupID == 0 {
		c.cancel()
		return fmt.Errorf("group_id is required for VK bot")
	}

	lp, err := longpoll.NewLongPoll(c.vk, groupID)
	if err != nil {
		c.cancel()
		return fmt.Errorf("failed to create long poll: %w", err)
	}
	c.lp = lp

	lp.MessageNew(func(_ context.Context, obj events.MessageNewObject) {
		c.handleMessage(obj.Message)
	})

	c.SetRunning(true)

	logger.InfoCF("vk", "VK bot connected", map[string]any{
		"group_id": groupID,
	})

	go func() {
		if err := lp.Run(); err != nil {
			logger.ErrorCF("vk", "Long poll failed", map[string]any{
				"error": err.Error(),
			})
		}
	}()

	return nil
}

func (c *VKChannel) Stop(ctx context.Context) error {
	logger.InfoC("vk", "Stopping VK bot...")
	c.SetRunning(false)

	if c.lp != nil {
		c.lp.Shutdown()
	}

	if c.cancel != nil {
		c.cancel()
	}

	return nil
}

func (c *VKChannel) handleMessage(msg object.MessagesMessage) {
	if msg.Action.Type != "" {
		return
	}

	if bool(msg.Out) {
		return
	}

	peerID := msg.PeerID
	chatID := strconv.Itoa(peerID)

	fromID := msg.FromID
	userID := strconv.Itoa(fromID)

	platformID := userID
	sender := bus.SenderInfo{
		Platform:    "vk",
		PlatformID:  platformID,
		CanonicalID: identity.BuildCanonicalID("vk", platformID),
		DisplayName: c.getUserName(fromID),
	}

	if !c.IsAllowedSender(sender) {
		logger.DebugCF("vk", "Message from unauthorized user", map[string]any{
			"peer_id": peerID,
		})
		return
	}

	mediaScope := channels.BuildMediaScope("vk", strconv.Itoa(msg.PeerID), strconv.Itoa(msg.ConversationMessageID))

	text := msg.Text
	var mediaPaths []string
	if len(msg.Attachments) > 0 {
		var attachText string
		attachText, mediaPaths = c.processAttachments(msg.Attachments, mediaScope)
		if text == "" {
			text = attachText
		}
	}

	if text == "" && len(mediaPaths) == 0 {
		return
	}
	if text == "" {
		text = "[media only]"
	}

	groupTrigger := c.bc.GroupTrigger
	isGroupChat := peerID != fromID

	if isGroupChat {
		isMentioned := c.isMentioned(msg)
		if isMentioned {
			text = c.stripBotMention(text)
		}
		respond, cleaned := c.ShouldRespondInGroup(isMentioned, text)
		if !respond {
			return
		}
		text = cleaned
		_ = groupTrigger
	}

	chatType := "direct"
	if isGroupChat {
		chatType = "group"
	}

	messageID := strconv.Itoa(msg.ConversationMessageID)

	metadata := map[string]string{
		"user_id":  userID,
		"is_group": fmt.Sprintf("%t", isGroupChat),
	}

	c.HandleInboundContext(c.ctx, chatID, text, mediaPaths, bus.InboundContext{
		Channel:   "vk",
		ChatID:    chatID,
		ChatType:  chatType,
		SenderID:  userID,
		MessageID: messageID,
		Mentioned: isGroupChat && c.isMentioned(msg),
		Raw:       metadata,
	}, sender)
}

func (c *VKChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}

	peerID, err := strconv.Atoi(msg.ChatID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID %s: %w", msg.ChatID, channels.ErrSendFailed)
	}

	if msg.Content == "" {
		return nil, nil
	}

	var messageIDs []string
	chunks := channels.SplitMessage(msg.Content, 4000)

	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}

		b := params.NewMessagesSendBuilder()
		b.Message(chunk)
		b.RandomID(0)
		b.PeerID(peerID)

		if msg.ReplyToMessageID != "" {
			if replyID, err := strconv.Atoi(msg.ReplyToMessageID); err == nil {
				b.ReplyTo(replyID)
			}
		}

		resp, err := c.vk.MessagesSend(b.Params)
		if err != nil {
			logger.ErrorCF("vk", "Failed to send message", map[string]any{
				"error":   err.Error(),
				"peer_id": peerID,
			})
			return messageIDs, fmt.Errorf("failed to send message: %w", err)
		}

		messageIDs = append(messageIDs, strconv.Itoa(resp))
	}

	return messageIDs, nil
}

// SendMedia implements channels.MediaSender: uploads each part to VK's
// message upload servers and sends them as message attachments. Captions are
// joined into the message text (VK attachments have no per-item caption).
func (c *VKChannel) SendMedia(
	ctx context.Context,
	msg bus.OutboundMediaMessage,
) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}

	peerID, err := strconv.Atoi(msg.ChatID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID %s: %w", msg.ChatID, channels.ErrSendFailed)
	}

	store := c.GetMediaStore()
	if store == nil {
		return nil, fmt.Errorf("no media store available: %w", channels.ErrSendFailed)
	}

	var attachments []string
	var captions []string
	for _, part := range msg.Parts {
		if part.Caption != "" {
			captions = append(captions, part.Caption)
		}

		localPath, err := store.Resolve(part.Ref)
		if err != nil {
			logger.ErrorCF("vk", "Failed to resolve media ref", map[string]any{
				"ref":   part.Ref,
				"error": err.Error(),
			})
			continue
		}

		file, err := os.Open(localPath)
		if err != nil {
			logger.ErrorCF("vk", "Failed to open media file", map[string]any{
				"path":  localPath,
				"error": err.Error(),
			})
			continue
		}

		filename := part.Filename
		if filename == "" {
			filename = filepath.Base(localPath)
		}

		var attachment string
		switch part.Type {
		case "image":
			var saved api.PhotosSaveMessagesPhotoResponse
			saved, err = c.vk.UploadMessagesPhoto(peerID, file)
			if err == nil && len(saved) > 0 {
				attachment = fmt.Sprintf("photo%d_%d", saved[0].OwnerID, saved[0].ID)
			}
		case "audio":
			var saved api.DocsSaveResponse
			saved, err = c.vk.UploadMessagesDoc(peerID, "audio_message", filename, "", file)
			if err == nil {
				attachment = vkDocAttachment(saved)
			}
		default: // "video" and "file" upload as documents
			var saved api.DocsSaveResponse
			saved, err = c.vk.UploadMessagesDoc(peerID, "doc", filename, "", file)
			if err == nil {
				attachment = vkDocAttachment(saved)
			}
		}
		file.Close()

		if err != nil {
			logger.ErrorCF("vk", "Failed to upload media", map[string]any{
				"type":  part.Type,
				"error": err.Error(),
			})
			return nil, fmt.Errorf("vk media upload: %w", channels.ErrTemporary)
		}
		if attachment == "" {
			logger.ErrorCF("vk", "Media upload returned no attachment", map[string]any{
				"type": part.Type,
			})
			continue
		}
		attachments = append(attachments, attachment)
	}

	if len(attachments) == 0 {
		return nil, fmt.Errorf("no media parts could be sent: %w", channels.ErrSendFailed)
	}

	b := params.NewMessagesSendBuilder()
	b.RandomID(0)
	b.PeerID(peerID)
	b.Attachment(strings.Join(attachments, ","))
	if text := strings.Join(captions, "\n"); text != "" {
		b.Message(text)
	}
	if msg.Context.ReplyToMessageID != "" {
		if replyID, err := strconv.Atoi(msg.Context.ReplyToMessageID); err == nil {
			b.ReplyTo(replyID)
		}
	}

	resp, err := c.vk.MessagesSend(b.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to send media message: %w", err)
	}

	return []string{strconv.Itoa(resp)}, nil
}

// vkDocAttachment builds a VK attachment reference from an upload response.
func vkDocAttachment(resp api.DocsSaveResponse) string {
	switch {
	case resp.Doc.ID != 0:
		return fmt.Sprintf("doc%d_%d", resp.Doc.OwnerID, resp.Doc.ID)
	case resp.AudioMessage.ID != 0:
		return fmt.Sprintf("doc%d_%d", resp.AudioMessage.OwnerID, resp.AudioMessage.ID)
	case resp.Graffiti.ID != 0:
		return fmt.Sprintf("doc%d_%d", resp.Graffiti.OwnerID, resp.Graffiti.ID)
	}
	return ""
}

func (c *VKChannel) isMentioned(msg object.MessagesMessage) bool {
	return false
}

func (c *VKChannel) stripBotMention(text string) string {
	return strings.TrimSpace(text)
}

func (c *VKChannel) getUserName(userID int) string {
	users, err := c.vk.UsersGet(api.Params{
		"user_ids": userID,
	})
	if err != nil || len(users) == 0 {
		return strconv.Itoa(userID)
	}

	user := users[0]
	return fmt.Sprintf("%s %s", user.FirstName, user.LastName)
}

// processAttachments converts VK attachments into text placeholders and
// downloadable media refs. Photos, docs, and voice messages are fetched into
// the media store; videos, stickers, and other types keep their placeholder.
func (c *VKChannel) processAttachments(
	attachments []object.MessagesMessageAttachment,
	mediaScope string,
) (string, []string) {
	var parts []string
	var mediaPaths []string

	storeMedia := func(localPath, filename string) string {
		if localPath == "" {
			return ""
		}
		if store := c.GetMediaStore(); store != nil {
			ref, err := store.Store(localPath, media.MediaMeta{
				Filename:      filename,
				Source:        "vk",
				CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
			}, mediaScope)
			if err == nil {
				return ref
			}
		}
		return localPath
	}

	for _, att := range attachments {
		switch att.Type {
		case "photo":
			parts = append(parts, "[photo]")
			if url := vkLargestPhotoURL(att.Photo); url != "" {
				localPath := utils.DownloadFile(url, "photo.jpg", utils.DownloadOptions{
					LoggerPrefix: "vk",
				})
				if ref := storeMedia(localPath, "photo.jpg"); ref != "" {
					mediaPaths = append(mediaPaths, ref)
				}
			}
		case "video":
			parts = append(parts, "[video]")
		case "audio":
			parts = append(parts, "[audio]")
		case "doc":
			if att.Doc.Title != "" {
				parts = append(parts, fmt.Sprintf("[document: %s]", att.Doc.Title))
			} else {
				parts = append(parts, "[document]")
			}
			if att.Doc.URL != "" {
				filename := att.Doc.Title
				if filename == "" {
					filename = "document"
				}
				localPath := utils.DownloadFile(att.Doc.URL, filename, utils.DownloadOptions{
					LoggerPrefix: "vk",
				})
				if ref := storeMedia(localPath, filename); ref != "" {
					mediaPaths = append(mediaPaths, ref)
				}
			}
		case "audio_message":
			parts = append(parts, "[voice]")
			voiceURL := att.AudioMessage.Preview.AudioMessage.LinkOgg
			filename := "voice.ogg"
			if voiceURL == "" {
				voiceURL = att.AudioMessage.Preview.AudioMessage.LinkMp3
				filename = "voice.mp3"
			}
			if voiceURL != "" {
				localPath := utils.DownloadFile(voiceURL, filename, utils.DownloadOptions{
					LoggerPrefix: "vk",
				})
				if ref := storeMedia(localPath, filename); ref != "" {
					mediaPaths = append(mediaPaths, ref)
				}
			}
		case "sticker":
			parts = append(parts, "[sticker]")
		}
	}

	return strings.Join(parts, " "), mediaPaths
}

// vkLargestPhotoURL returns the URL of the largest available photo size.
func vkLargestPhotoURL(photo object.PhotosPhoto) string {
	best := ""
	bestArea := float64(0)
	for _, size := range photo.Sizes {
		if size.URL == "" {
			continue
		}
		area := size.Width * size.Height
		if area >= bestArea {
			bestArea = area
			best = size.URL
		}
	}
	return best
}

func (c *VKChannel) VoiceCapabilities() channels.VoiceCapabilities {
	return channels.VoiceCapabilities{ASR: true, TTS: true}
}
