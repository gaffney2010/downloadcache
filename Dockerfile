# --- Builder Stage ---
# Use the official Go image as a builder.
FROM golang:1.24-alpine AS builder

# Set the working directory inside the container.
WORKDIR /app

# Copy go.mod and go.sum files to leverage Docker cache layers.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code.
COPY . .

# Build the Go application.
# -o /bin/server builds the output binary to /bin/server.
# CGO_ENABLED=0 is important for creating a static binary.
# -ldflags="-s -w" strips debugging information, making the binary smaller.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/server ./server

# --- Final Stage ---
# Use a minimal base image for the final container.
FROM alpine:3.19

# Create a non-root user and group for security.
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Create the cache directory and set its ownership to the non-root user.
# This ensures our application can write to the mounted volume.
RUN mkdir /cache && chown appuser:appgroup /cache

# Copy the compiled binary from the builder stage.
COPY --from=builder /bin/server /bin/server

# Expose the gRPC port.
EXPOSE 50051

# Define the cache directory as a volume.
# This allows users to easily mount a host directory for persistent caching.
VOLUME /cache

# Switch to the non-root user.
USER appuser

# Set the command to run when the container starts.
# The server will use /cache by default unless CACHE_DIR is overridden.
CMD ["/bin/server"]
