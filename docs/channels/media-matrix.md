# Channel Media Matrix

Attachment support across channels. **Inbound** means the channel downloads
attachments into the media store and passes `media://` refs on the inbound
message. **Outbound** means the channel implements `channels.MediaSender`
(`SendMedia`) and can deliver `bus.OutboundMediaMessage` parts.

| Channel | Inbound | Outbound | Notes |
|---|---|---|---|
| telegram | ✅ | ✅ | Photos, audio, voice bubbles, video, documents, media groups |
| discord | ✅ | ✅ | |
| slack | ✅ | ✅ | |
| feishu | ✅ | ✅ | |
| wecom | ✅ | ✅ | |
| weixin | ✅ | ✅ | |
| qq | ✅ | ✅ | |
| deltachat | ✅ | ✅ | |
| line | ✅ | ✅ | |
| matrix | ✅ | ✅ | |
| onebot | ✅ | ✅ | |
| pico / pico_client | ✅ | ✅ | Native protocol carries refs |
| whatsapp (bridge) | ✅ | ✅ | Outbound parts sent as base64 `{"type":"media"}` frames over the bridge WebSocket |
| whatsapp_native | ✅ | ✅ | whatsmeow upload + `ImageMessage`/`VideoMessage`/`AudioMessage`/`DocumentMessage`; requires `-tags whatsapp_native` |
| vk | ✅ | ✅ | Inbound photos/docs/voice downloaded; outbound via message upload servers (photos + docs) |
| irc | — | — | Text-only protocol |
| mqtt | — | — | Text-only protocol |
| maixcam | — | — | Text-only device channel |
| teams_webhook | — | — | Outgoing webhook, text/cards only |
| slack_webhook | — | — | Outgoing webhook, text only |

## Notes

- `MediaPart.Type` is `image` | `audio` | `video` | `file`; `Ref` resolves
  through `media.MediaStore` (`media://<id>` → local path).
- Channels that cannot represent a media type natively send it as a generic
  file/document attachment (VK docs).
- Captions are per-part where the platform supports them (Telegram, WhatsApp
  native). VK joins captions into the message text.
- Inbound `Media` entries are `media://` refs when a media store is injected,
  or raw local paths otherwise.
