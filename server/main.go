package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	pb "downloadcache/pb" // Adjust to your actual go module path

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tebeka/selenium"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	defaultPort     = "50051"
	defaultCacheDir = "/cache" // This path will be used inside the Docker container
)

// downloadCacheServer implements the DownloadCacheServiceServer interface.
type downloadCacheServer struct {
	pb.UnimplementedDownloadCacheServer
	cacheDir    string
	minifier    *minify.M
	fetchMode   string   // "http" or "selenium"
	seleniumURL string   // Stores the URL to the remote Selenium instance
	urlLocks    sync.Map // Used to prevent concurrent downloads of the same URL
}

// newServer creates a new instance of our server.
func newServer(cacheDir string, fetchMode string, seleniumURL string) (*downloadCacheServer, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	m := minify.New()
	m.AddFunc("text/html", html.Minify)

	log.Printf("Cache directory initialized at: %s", cacheDir)

	return &downloadCacheServer{
		cacheDir:    cacheDir,
		minifier:    m,
		fetchMode:   fetchMode,
		seleniumURL: seleniumURL,
	}, nil
}

// sanitizeURLForFilename creates a safe filename from a URL.
func sanitizeURLForFilename(rawURL string) string {
	return url.PathEscape(rawURL)
}

// Get handles the gRPC request.
func (s *downloadCacheServer) Get(ctx context.Context, req *pb.DownloadCacheRequest) (*pb.DownloadCacheResponse, error) {
	log.Printf("Received request for URL: %s, Invalidate: %v", req.GetUrl(), req.GetInvalidate())

	if req.GetUrl() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "URL cannot be empty")
	}

	cacheKey := sanitizeURLForFilename(req.GetUrl())
	cacheFilePath := filepath.Join(s.cacheDir, cacheKey)

	// --- Cache Check ---
	if !req.GetInvalidate() {
		if _, err := os.Stat(cacheFilePath); err == nil {
			log.Printf("Cache HIT for URL: %s", req.GetUrl())
			content, err := s.readFromCache(cacheFilePath)
			if err != nil {
				log.Printf("Failed to read from cache, proceeding to download: %v", err)
			} else {
				return &pb.DownloadCacheResponse{PageContents: content}, nil
			}
		}
	}

	// --- Download & Process ---
	log.Printf("Cache MISS or invalidation for URL: %s", req.GetUrl())
	return s.downloadAndCache(req.GetUrl(), cacheFilePath, req.GetUserAgent(), req.GetSkipMinify())
}

// fetchWithHTTP fetches a URL using a plain HTTP GET request.
func (s *downloadCacheServer) fetchWithHTTP(rawURL string, userAgent string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP GET returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response body: %w", err)
	}
	return body, nil
}

// fetchWithSelenium fetches a URL using a remote Selenium WebDriver.
func (s *downloadCacheServer) fetchWithSelenium(rawURL string, userAgent string) ([]byte, error) {
	caps := selenium.Capabilities{"browserName": "chrome"}
	args := []string{
		"--headless",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-gpu",
	}
	if userAgent != "" {
		args = append(args, "--user-agent="+userAgent)
	}
	chromeCaps := map[string]interface{}{
		"args": args,
	}
	caps["goog:chromeOptions"] = chromeCaps

	wd, err := selenium.NewRemote(caps, s.seleniumURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open session with WebDriver: %w", err)
	}
	defer func() {
		if err := wd.Quit(); err != nil {
			log.Printf("Failed to quit WebDriver session: %v", err)
		}
	}()

	log.Printf("Fetching URL with Selenium: %s", rawURL)
	if err := wd.Get(rawURL); err != nil {
		return nil, fmt.Errorf("failed to navigate to URL with Selenium %s: %w", rawURL, err)
	}

	// Wait for JS to render.
	time.Sleep(2 * time.Second)

	pageSource, err := wd.PageSource()
	if err != nil {
		return nil, fmt.Errorf("failed to get page source from Selenium: %w", err)
	}
	return []byte(pageSource), nil
}

// downloadAndCache handles the logic for downloading, processing, and caching a URL.
func (s *downloadCacheServer) downloadAndCache(rawURL, cacheFilePath string, userAgent string, skipMinify bool) (*pb.DownloadCacheResponse, error) {
	// Lock per URL to ensure only one goroutine downloads a specific URL at a time.
	mu, _ := s.urlLocks.LoadOrStore(rawURL, &sync.Mutex{})
	mutex := mu.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	defer s.urlLocks.Delete(rawURL) // Clean up the map after use

	// Double-check cache: another request might have finished while we waited for the lock.
	if _, err := os.Stat(cacheFilePath); err == nil {
		log.Printf("Cache HIT (after lock) for URL: %s", rawURL)
		content, err := s.readFromCache(cacheFilePath)
		if err == nil {
			return &pb.DownloadCacheResponse{PageContents: content}, nil
		}
	}

	// Fetch page content using the configured mode.
	var bodyBytes []byte
	var err error
	switch s.fetchMode {
	case "selenium":
		bodyBytes, err = s.fetchWithSelenium(rawURL, userAgent)
	default:
		bodyBytes, err = s.fetchWithHTTP(rawURL, userAgent)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fetch failed: %v", err)
	}

	// Minify the content unless the caller opted out.
	var minifiedBytes []byte
	if skipMinify {
		minifiedBytes = bodyBytes
	} else {
		minifiedBytes, err = s.minifier.Bytes("text/html", bodyBytes)
		if err != nil {
			log.Printf("Warning: failed to minify content for %s, using original. Error: %v", rawURL, err)
			minifiedBytes = bodyBytes
		}
	}

	// Write the minified and gzipped content to the cache file.
	if err := s.writeToCache(cacheFilePath, minifiedBytes); err != nil {
		log.Printf("Error: failed to write to cache file %s: %v", cacheFilePath, err)
	} else {
		log.Printf("Successfully cached content for %s", rawURL)
	}

	return &pb.DownloadCacheResponse{PageContents: string(minifiedBytes)}, nil
}

// readFromCache reads and decompresses content from a cache file.
func (s *downloadCacheServer) readFromCache(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzipReader.Close()

	content, err := io.ReadAll(gzipReader)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// writeToCache compresses and writes content to a cache file.
func (s *downloadCacheServer) writeToCache(path string, content []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	_, err = gzipWriter.Write(content)
	return err
}

func main() {
	// --- Get config from environment variables ---
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = defaultCacheDir
	}

	fetchMode := os.Getenv("FETCH_MODE")
	if fetchMode == "" {
		fetchMode = "http"
	}

	seleniumURL := os.Getenv("SELENIUM_URL")
	if fetchMode == "selenium" && seleniumURL == "" {
		log.Fatalf("SELENIUM_URL environment variable is required when FETCH_MODE=selenium")
	}

	log.Printf("Starting with FETCH_MODE=%s", fetchMode)

	// --- Start gRPC Server ---
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	server, err := newServer(cacheDir, fetchMode, seleniumURL)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	pb.RegisterDownloadCacheServer(grpcServer, server)
	// Enable reflection for tools like grpcurl to inspect the service.
	reflection.Register(grpcServer)

	log.Printf("gRPC server listening on port %s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
