#!/usr/bin/env bash
# acp-container suite: installs a docker CLI in the golang runner image,
# builds the ACP agent fixture image on the shared host daemon (via the
# mounted socket), then drives ClientManager.RunAgent through the
# runtime=container path end-to-end.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq docker-cli >/dev/null 2>&1 \
	|| apt-get install -y -qq docker.io >/dev/null

docker version >/dev/null

docker build -f integration/fixtures/acp-agent/Dockerfile \
	-t rhizome-acp-fixture:latest .

export RHIZOME_ACP_CONTAINER_IT=1
export RHIZOME_ACP_FIXTURE_IMAGE=rhizome-acp-fixture:latest
go test ./pkg/acp -run TestIntegrationACPContainerRuntime -v -count=1
