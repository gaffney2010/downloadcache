# downloadcache

A gRPC service that downloads, minifies, and caches web pages. Supports two fetch modes: a simple HTTP client (default) or Selenium for JavaScript-rendered pages.

## Build

```bash
go mod init downloadcache
go mod tidy
```

Build the Docker image:

```bash
docker build -t downloadcache-service .
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `50051` | gRPC listen port |
| `CACHE_DIR` | `/cache` | Directory for cached pages |
| `FETCH_MODE` | `http` | `"http"` for plain HTTP GET, `"selenium"` for browser-based fetch |
| `SELENIUM_URL` | _(none)_ | Selenium WebDriver URL (required when `FETCH_MODE=selenium`) |

## Docker Compose examples

### HTTP mode (simple, no Selenium needed)

```yaml
version: '3.8'

services:
  downloadcache:
    build:
      context: https://github.com/gaffney2010/downloadcache.git
    container_name: downloadcache_service
    restart: unless-stopped
    ports:
      - "50051:50051"
    volumes:
      - ./my_cache:/cache
    environment:
      - FETCH_MODE=http
```

### Selenium mode (for JS-rendered pages)

```yaml
version: '3.8'

services:
  selenium:
    image: selenium/standalone-chrome:latest
    container_name: selenium_chrome
    shm_size: '2g'
    networks:
      - app-network

  downloadcache:
    build:
      context: https://github.com/gaffney2010/downloadcache.git
    container_name: downloadcache_service
    restart: unless-stopped
    ports:
      - "50051:50051"
    volumes:
      - ./my_cache:/cache
    environment:
      - FETCH_MODE=selenium
      - SELENIUM_URL=http://selenium:4444/wd/hub
    depends_on:
      - selenium
    networks:
      - app-network

networks:
  app-network:
    driver: bridge
```
