# --- Builder Stage ---
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/server ./

# --- Final Stage ---
FROM alpine:3.19

RUN addgroup -S appgroup && adduser -S appuser -G appgroup
RUN mkdir /cache && chown appuser:appgroup /cache

COPY --from=builder /bin/server /bin/server

EXPOSE 50051
VOLUME /cache
USER appuser

CMD ["/bin/server"]