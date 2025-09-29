# downloadcache

Build with
# In the downloadcache/ directory
go mod init downloadcache # Or your preferred module path
go mod tidy

Build docker container:
docker build -t downloadcache-service .

# Use in a docker-compose

Need to depend on a Selenium container.  For example.

```
version: '3.8'

services:
  # The official Selenium service
  selenium:
    image: selenium/standalone-chrome:latest # Use the official standalone Chrome image
    container_name: selenium_chrome
    shm_size: '2g' # Recommended to avoid browser crashes in Docker
    networks:
      - app-network

  downloadcache:
    build:
      context: https://github.com/gaffney2010/downloadcache.git
    container_name: downloadcache_service
    restart: unless-stopped
    ports:
      - "50051:50051" # Expose to host for debugging (e.g., with grpcurl)
    volumes:
      - ./my_cache:/cache # Mount a local directory for persistent caching
    environment:
      # This is how your Go app finds the Selenium container.
      # "selenium" is the service name defined above.
      - SELENIUM_URL=http://selenium:4444/wd/hub
    depends_on:
      - selenium # Ensures the selenium service starts before your app
    networks:
      - app-network

networks:
  app-network:
    driver: bridge
```
