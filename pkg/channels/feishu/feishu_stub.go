//go:build !feishu || (feishu && !amd64 && !arm64 && !riscv64 && !mips64 && !ppc64)

package feishu

import (
	"context"
	"errors"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/channels"
	"github.com/stpinkie/rhizome/pkg/config"
)

// FeishuChannel is a stub implementation used when the feishu build tag is not
// set or when the target architecture does not support the Lark SDK.
type FeishuChannel struct {
	*channels.BaseChannel
}

var errUnsupported = errors.New("feishu channel is not compiled in")

// NewFeishuChannel returns an error when Feishu support is not compiled in.
// Build with: go build -tags feishu ./cmd/...
func NewFeishuChannel(bc *config.Channel, cfg *config.FeishuSettings, bus *bus.MessageBus) (*FeishuChannel, error) {
	_ = bc
	_ = cfg
	_ = bus
	return nil, errors.New(
		"feishu channel is not compiled in; build with -tags feishu on a 64-bit architecture",
	)
}

// Start is a stub method to satisfy the Channel interface
func (c *FeishuChannel) Start(ctx context.Context) error {
	return errUnsupported
}

// Stop is a stub method to satisfy the Channel interface
func (c *FeishuChannel) Stop(ctx context.Context) error {
	return errUnsupported
}

// Send is a stub method to satisfy the Channel interface
func (c *FeishuChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	return nil, errUnsupported
}

// EditMessage is a stub method to satisfy MessageEditor
func (c *FeishuChannel) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	return errUnsupported
}

// SendPlaceholder is a stub method to satisfy PlaceholderCapable
func (c *FeishuChannel) SendPlaceholder(ctx context.Context, chatID string) (string, error) {
	return "", errUnsupported
}

// ReactToMessage is a stub method to satisfy ReactionCapable
func (c *FeishuChannel) ReactToMessage(ctx context.Context, chatID, messageID string) (func(), error) {
	return func() {}, errUnsupported
}

// SendMedia is a stub method to satisfy MediaSender
func (c *FeishuChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	return nil, errUnsupported
}
