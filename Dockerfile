# Use Alpine-based Go image for building
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency files and download
COPY go.mod go.sum* ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build -o /epoch main.go

# Use a minimal Alpine image for final production runtime
FROM alpine:latest

# Install necessary packages for health checks or debugging if needed
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /

# Copy binary from builder stage
COPY --from=builder /epoch /epoch

# Expose port and define entry point
EXPOSE 8080

CMD ["/epoch"]
