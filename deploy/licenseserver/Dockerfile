FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /licenseserver ./cmd/licenseserver

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /licenseserver /usr/local/bin/licenseserver
EXPOSE 8080
ENTRYPOINT ["licenseserver"]
CMD ["serve"]
