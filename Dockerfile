# syntax=docker/dockerfile:1
# Multi-stage build. TARGETOS/TARGETARCH are populated automatically by
# `docker buildx build --platform ...`.

FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -o /out/llm-gateway ./cmd/llm-gateway

FROM alpine:3.20
RUN addgroup -S gateway && adduser -S -G gateway gateway
COPY --from=build /out/llm-gateway /usr/local/bin/llm-gateway

USER gateway
WORKDIR /app
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/llm-gateway"]
CMD ["--config", "/app/config.yaml"]
