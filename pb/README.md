Rebuild with (run from parent dir)
```
protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    pb/downloadcache.proto
```

(Make sure that Go bin directory is in your path
export PATH="$PATH:$(go env GOPATH)/bin"
)
