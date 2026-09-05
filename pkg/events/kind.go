package events

const (
	// KindAgentTurnStart is emitted when an agent turn starts.
	KindAgentTurnStart Kind = "agent.turn.start"
	// KindAgentTurnEnd is emitted when an agent turn ends.
	KindAgentTurnEnd Kind = "agent.turn.end"

	// KindAgentLLMRequest is emitted before an LLM request.
	KindAgentLLMRequest Kind = "agent.llm.request"
	// KindAgentLLMDelta is emitted for streaming LLM deltas.
	KindAgentLLMDelta Kind = "agent.llm.delta"
	// KindAgentLLMResponse is emitted after an LLM response.
	KindAgentLLMResponse Kind = "agent.llm.response"
	// KindAgentLLMRetry is emitted before retrying an LLM request.
	KindAgentLLMRetry Kind = "agent.llm.retry"

	// KindAgentContextCompress is emitted when agent context is compressed.
	KindAgentContextCompress Kind = "agent.context.compress"
	// KindAgentSessionSummarize is emitted when session summarization completes.
	KindAgentSessionSummarize Kind = "agent.session.summarize"

	// KindAgentToolExecStart is emitted before a tool executes.
	KindAgentToolExecStart Kind = "agent.tool.exec_start"
	// KindAgentToolExecEnd is emitted after a tool finishes.
	KindAgentToolExecEnd Kind = "agent.tool.exec_end"
	// KindAgentToolExecSkipped is emitted when a tool call is skipped.
	KindAgentToolExecSkipped Kind = "agent.tool.exec_skipped"

	// KindAgentSteeringInjected is emitted when steering is injected into context.
	KindAgentSteeringInjected Kind = "agent.steering.injected"
	// KindAgentFollowUpQueued is emitted when async follow-up input is queued.
	KindAgentFollowUpQueued Kind = "agent.follow_up.queued"
	// KindAgentInterruptReceived is emitted when a turn interrupt is accepted.
	KindAgentInterruptReceived Kind = "agent.interrupt.received"

	// KindAgentSubTurnSpawn is emitted when a sub-turn is spawned.
	KindAgentSubTurnSpawn Kind = "agent.subturn.spawn"
	// KindAgentSubTurnEnd is emitted when a sub-turn ends.
	KindAgentSubTurnEnd Kind = "agent.subturn.end"
	// KindAgentSubTurnResultDelivered is emitted when a sub-turn result is delivered.
	KindAgentSubTurnResultDelivered Kind = "agent.subturn.result_delivered"
	// KindAgentSubTurnOrphan is emitted when a sub-turn result cannot be delivered.
	KindAgentSubTurnOrphan Kind = "agent.subturn.orphan"
	// KindAgentError is emitted when agent execution reports an error.
	KindAgentError Kind = "agent.error"

	// KindChannelLifecycleStarted is emitted when a channel starts.
	KindChannelLifecycleStarted Kind = "channel.lifecycle.started"
	// KindChannelLifecycleInitialized is emitted when a channel is initialized.
	KindChannelLifecycleInitialized Kind = "channel.lifecycle.initialized"
	// KindChannelLifecycleStartFailed is emitted when a channel fails to start.
	KindChannelLifecycleStartFailed Kind = "channel.lifecycle.start_failed"
	// KindChannelLifecycleStopped is emitted when a channel stops.
	KindChannelLifecycleStopped Kind = "channel.lifecycle.stopped"
	// KindChannelWebhookRegistered is emitted when a channel webhook is registered.
	KindChannelWebhookRegistered Kind = "channel.webhook.registered"
	// KindChannelWebhookUnregistered is emitted when a channel webhook is unregistered.
	KindChannelWebhookUnregistered Kind = "channel.webhook.unregistered"
	// KindChannelMessageOutboundQueued is emitted when an outbound message is queued.
	KindChannelMessageOutboundQueued Kind = "channel.message.outbound_queued"
	// KindChannelMessageOutboundSent is emitted when an outbound channel message is sent.
	KindChannelMessageOutboundSent Kind = "channel.message.outbound_sent"
	// KindChannelMessageOutboundFailed is emitted when an outbound channel message fails.
	KindChannelMessageOutboundFailed Kind = "channel.message.outbound_failed"
	// KindChannelRateLimited is emitted when channel rate limiting blocks delivery.
	KindChannelRateLimited Kind = "channel.rate_limited"

	// KindBusPublishFailed is emitted when message bus publish fails.
	KindBusPublishFailed Kind = "bus.publish.failed"
	// KindBusMessageDropped is emitted when a message is dropped due to
	// backpressure (channel full for longer than the drop budget).
	KindBusMessageDropped Kind = "bus.message.dropped"
	// KindBusCloseStarted is emitted when message bus close starts.
	KindBusCloseStarted Kind = "bus.close.started"
	// KindBusCloseCompleted is emitted when message bus close completes.
	KindBusCloseCompleted Kind = "bus.close.completed"
	// KindBusCloseDrained is emitted when message bus close drains buffered messages.
	KindBusCloseDrained Kind = "bus.close.drained"

	// KindGatewayStart is emitted when gateway startup reaches runtime bootstrap.
	KindGatewayStart Kind = "gateway.start"
	// KindGatewayReady is emitted when gateway services are started and ready.
	KindGatewayReady Kind = "gateway.ready"
	// KindGatewayShutdown is emitted when gateway shutdown starts.
	KindGatewayShutdown Kind = "gateway.shutdown"
	// KindGatewayReloadStarted is emitted when gateway reload starts.
	KindGatewayReloadStarted Kind = "gateway.reload.started"
	// KindGatewayReloadCompleted is emitted when gateway reload completes.
	KindGatewayReloadCompleted Kind = "gateway.reload.completed"
	// KindGatewayReloadFailed is emitted when gateway reload fails.
	KindGatewayReloadFailed Kind = "gateway.reload.failed"

	// KindMCPServerConnected is emitted when an MCP server connects.
	KindMCPServerConnected Kind = "mcp.server.connected"
	// KindMCPServerConnecting is emitted before connecting to an MCP server.
	KindMCPServerConnecting Kind = "mcp.server.connecting"
	// KindMCPServerFailed is emitted when an MCP server fails.
	KindMCPServerFailed Kind = "mcp.server.failed"
	// KindMCPToolDiscovered is emitted when an MCP tool is discovered.
	KindMCPToolDiscovered Kind = "mcp.tool.discovered"
	// KindMCPToolCallStart is emitted when an MCP tool call starts.
	KindMCPToolCallStart Kind = "mcp.tool.call.start"
	// KindMCPToolCallEnd is emitted when an MCP tool call ends.
	KindMCPToolCallEnd Kind = "mcp.tool.call.end"

	// Mesh / P2P events

	// KindMeshPeerConnected is emitted when the local node connects to a peer.
	KindMeshPeerConnected Kind = "mesh.peer.connected"
	// KindMeshPeerDisconnected is emitted when the local node disconnects from a peer.
	KindMeshPeerDisconnected Kind = "mesh.peer.disconnected"
	// KindMeshCapabilityReceived is emitted when a capability announcement is received from a peer.
	KindMeshCapabilityReceived Kind = "mesh.cap.received"
	// KindMeshCapabilityQueried is emitted when a peer queries our capability.
	KindMeshCapabilityQueried Kind = "mesh.cap.queried"
	// KindMeshDHTBootstrapStart is emitted when the DHT bootstrap begins.
	KindMeshDHTBootstrapStart Kind = "mesh.dht.bootstrap.start"
	// KindMeshDHTBootstrapDone is emitted when the DHT bootstrap finishes.
	KindMeshDHTBootstrapDone Kind = "mesh.dht.bootstrap.done"
	// KindMeshDHTDiscovered is emitted when the DHT discovers a provider for the rendezvous.
	KindMeshDHTDiscovered Kind = "mesh.dht.discovered"
	// KindMeshRemoteDelegateStart is emitted when a remote delegate request begins.
	KindMeshRemoteDelegateStart Kind = "mesh.remote.delegate.start"
	// KindMeshRemoteDelegateEnd is emitted when a remote delegate request ends.
	KindMeshRemoteDelegateEnd Kind = "mesh.remote.delegate.end"
	// KindMeshRemoteSpawnStart is emitted when a remote spawn request begins.
	KindMeshRemoteSpawnStart Kind = "mesh.remote.spawn.start"
	// KindMeshRemoteSpawnEnd is emitted when a remote spawn request ends.
	KindMeshRemoteSpawnEnd Kind = "mesh.remote.spawn.end"
	// KindMeshTaskSubmit is emitted when an async mesh task is submitted to or
	// accepted from a peer over /rhizome/agent-task/1.0.0.
	KindMeshTaskSubmit Kind = "mesh.task.submit"
	// KindMeshTaskUpdate is emitted when a mesh task changes status
	// (running, done, error, cancelled).
	KindMeshTaskUpdate Kind = "mesh.task.update"
	// KindMeshReachabilityChanged is emitted when the node's detected NAT
	// reachability changes (public, private, unknown).
	KindMeshReachabilityChanged Kind = "mesh.reachability.changed"
	// KindMeshRelayReservation is emitted when the set of relayed
	// (/p2p-circuit) addresses advertised by this node changes.
	KindMeshRelayReservation Kind = "mesh.relay.reservation"
	// KindMeshRemoteAudit is emitted for every remote agent request outcome
	// (accepted, rejected, rate-limited, completed, failed).
	KindMeshRemoteAudit Kind = "mesh.remote.audit"
	// KindMeshCapabilityUnsigned is emitted when a trusted peer sends an
	// unsigned capability manifest (rejected unless require_signed_caps is off).
	KindMeshCapabilityUnsigned Kind = "mesh.cap.unsigned"
	// KindMeshError is emitted when a mesh operation fails.
	KindMeshError Kind = "mesh.error"
)

var knownKinds = []Kind{
	KindAgentTurnStart,
	KindAgentTurnEnd,
	KindAgentLLMRequest,
	KindAgentLLMDelta,
	KindAgentLLMResponse,
	KindAgentLLMRetry,
	KindAgentContextCompress,
	KindAgentSessionSummarize,
	KindAgentToolExecStart,
	KindAgentToolExecEnd,
	KindAgentToolExecSkipped,
	KindAgentSteeringInjected,
	KindAgentFollowUpQueued,
	KindAgentInterruptReceived,
	KindAgentSubTurnSpawn,
	KindAgentSubTurnEnd,
	KindAgentSubTurnResultDelivered,
	KindAgentSubTurnOrphan,
	KindAgentError,
	KindChannelLifecycleStarted,
	KindChannelLifecycleInitialized,
	KindChannelLifecycleStartFailed,
	KindChannelLifecycleStopped,
	KindChannelWebhookRegistered,
	KindChannelWebhookUnregistered,
	KindChannelMessageOutboundQueued,
	KindChannelMessageOutboundSent,
	KindChannelMessageOutboundFailed,
	KindChannelRateLimited,
	KindBusPublishFailed,
	KindBusMessageDropped,
	KindBusCloseStarted,
	KindBusCloseCompleted,
	KindBusCloseDrained,
	KindGatewayStart,
	KindGatewayReady,
	KindGatewayShutdown,
	KindGatewayReloadStarted,
	KindGatewayReloadCompleted,
	KindGatewayReloadFailed,
	KindMCPServerConnected,
	KindMCPServerConnecting,
	KindMCPServerFailed,
	KindMCPToolDiscovered,
	KindMCPToolCallStart,
	KindMCPToolCallEnd,
	KindMeshPeerConnected,
	KindMeshPeerDisconnected,
	KindMeshCapabilityReceived,
	KindMeshCapabilityQueried,
	KindMeshDHTBootstrapStart,
	KindMeshDHTBootstrapDone,
	KindMeshDHTDiscovered,
	KindMeshRemoteDelegateStart,
	KindMeshRemoteDelegateEnd,
	KindMeshRemoteSpawnStart,
	KindMeshRemoteSpawnEnd,
	KindMeshTaskSubmit,
	KindMeshTaskUpdate,
	KindMeshReachabilityChanged,
	KindMeshRelayReservation,
	KindMeshRemoteAudit,
	KindMeshCapabilityUnsigned,
	KindMeshError,
}

// KnownKinds returns the runtime event kinds declared by this package.
func KnownKinds() []Kind {
	return append([]Kind(nil), knownKinds...)
}
