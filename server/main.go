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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const (
	defaultPort      = "50051"
	defaultCacheDir  = "/cache" // This path will be used inside the Docker container
	downloadTimeout  = 0 * time.Second
)

// downloadCacheServer implements the DownloadCacheServiceServer interface.
type downloadCacheServer struct {
	pb.UnimplementedDownloadCacheServer
	cacheDir string
	minifier *minify.M
	urlLocks sync.Map // Used to prevent concurrent downloads of the same URL
}

// newServer creates a new instance of our server.
func newServer(cacheDir string) (*downloadCacheServer, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	m := minify.New()
	m.AddFunc("text/html", html.Minify)

	log.Printf("Cache directory initialized at: %s", cacheDir)

	return &downloadCacheServer{
		cacheDir: cacheDir,
		minifier: m,
	}, nil
}

// sanitizeURLForFilename creates a safe filename from a URL.
// We use URL path escaping which is robust and reversible if needed.
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
		// Check if the file exists.
		if _, err := os.Stat(cacheFilePath); err == nil {
			log.Printf("Cache HIT for URL: %s", req.GetUrl())
			content, err := s.readFromCache(cacheFilePath)
			if err != nil {
				// If reading fails, treat it as a cache miss and proceed to download.
				log.Printf("Failed to read from cache, proceeding to download: %v", err)
			} else {
				return &pb.DownloadCacheResponse{PageContents: content}, nil
			}
		}
	}

	// --- Download & Process ---
	log.Printf("Cache MISS or invalidation for URL: %s", req.GetUrl())
	return s.downloadAndCache(req.GetUrl(), cacheFilePath)
}

// downloadAndCache handles the logic for downloading, processing, and caching a URL.
// It includes a lock to prevent a "thundering herd" problem for the same URL.
func (s *downloadCacheServer) downloadAndCache(rawURL, cacheFilePath string) (*pb.DownloadCacheResponse, error) {
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

	// Create a context with a timeout for the download.
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	// Perform the HTTP GET request.
	httpReq, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create HTTP request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to download URL %s: %v", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, status.Errorf(codes.Internal, "download failed with status code %d for URL %s", resp.StatusCode, rawURL)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to read response body: %v", err)
	}

	// Minify the content. We use MinifyBytes which is more direct.
	minifiedBytes, err := s.minifier.Bytes("text/html", bodyBytes)
	if err != nil {
		log.Printf("Warning: failed to minify content for %s, using original. Error: %v", rawURL, err)
		minifiedBytes = bodyBytes // Fallback to original content
	}

	// Write the minified and gzipped content to the cache file.
	if err := s.writeToCache(cacheFilePath, minifiedBytes); err != nil {
		// Log the error but don't fail the request, as we can still serve the content.
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
	// Get config from environment variables with defaults
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = defaultCacheDir
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	server, err := newServer(cacheDir)
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
