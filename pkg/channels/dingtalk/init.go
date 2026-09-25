package dingtalk

import (
	"errors"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/channels"
	"github.com/stpinkie/rhizome/pkg/config"
)

// DingTalk integration is removed pending an upstream fix for
// dingtalk-stream-sdk-go (#3382: panic "send on closed channel" inside the
// SDK's processLoop goroutine, unrecoverable from the caller). The channel
// type stays registered in pkg/config so existing channel_list.dingtalk
// entries still validate; this stub fails initialization with a clear error
// instead of connecting. See docs/project/upstream-issue-triage.md.
func init() {
	channels.RegisterFactory(
		config.ChannelDingTalk,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			return nil, errors.New(
				"dingtalk channel removed pending upstream dingtalk-stream-sdk-go fix; " +
					"see docs/project/upstream-issue-triage.md",
			)
		},
	)
}
