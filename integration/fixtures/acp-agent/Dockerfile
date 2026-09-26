FROM golang:1.26.6-bookworm AS build

ENV GOTOOLCHAIN=auto

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -tags goolm,stdjson -o /out/acp-agent ./integration/fixtures/acp-agent

FROM alpine:3.22

RUN adduser -D -u 10001 appuser

COPY --from=build /out/acp-agent /usr/local/bin/acp-agent

USER appuser

ENTRYPOINT ["/usr/local/bin/acp-agent"]
