# Stage 1: Build the binary with cgo enabled.
FROM golang:1.23-alpine AS builder

# Install build dependencies for cgo and sqlite.
RUN apk add --no-cache git gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go.mod and go.sum first for caching dependencies.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application code.
COPY . .

# Build the binary with cgo enabled.
RUN CGO_ENABLED=1 GOOS=linux go build -a -o tradingview_apiservice .

# Stage 2: Create the final image.
FROM alpine:latest

# Install the SQLite runtime library.
RUN apk add --no-cache sqlite-libs

# Copy the binary from the builder stage.
COPY --from=builder /app/tradingview_apiservice /tradingview_apiservice

# Copy the entrypoint script into the image.
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose port 8090 (adjust if needed).
EXPOSE 8090

# We rely on the environment variable "GOOGLE_CREDS_BASE64" 
# (set via `fly secrets set` or elsewhere).
ENV GOOGLE_CREDS_BASE64=""

# Run the entrypoint, which writes credentials.json (if available) and runs the app.
ENTRYPOINT ["/entrypoint.sh"]