# Stage 1: Build the Go binary
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /satsbook ./cmd/satsbook

# Stage 2: Minimal runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /satsbook /usr/local/bin/satsbook

# Data directory for SQLite
RUN mkdir -p /data
VOLUME /data

EXPOSE 3000

ENTRYPOINT ["satsbook"]
