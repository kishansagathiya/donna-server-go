# go.mod / MCP SDK require Go 1.25+.
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/server .
# Railway routes to the PORT env var at runtime (typically 8080).
EXPOSE 8080
CMD ["./server"]
