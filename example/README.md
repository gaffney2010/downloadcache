# DownloadCache Python Client Example

A Python client for the DownloadCache gRPC service.

## Prerequisites

Build and run the Docker container from the repo root:

```bash
docker build -t downloadcache-service .
docker run -p 50051:50051 downloadcache-service
```

## Setup

Install dependencies and generate the Python gRPC stubs from the proto file:

```bash
pip install -r requirements.txt

python -m grpc_tools.protoc \
  -I../pb \
  --python_out=. \
  --grpc_python_out=. \
  ../pb/downloadcache.proto
```

This generates `downloadcache_pb2.py` and `downloadcache_pb2_grpc.py`.

## Usage

Fetch a URL through the cache:

```bash
python client.py https://example.com
```

Force a cache miss (re-download):

```bash
python client.py --invalidate https://example.com
```

Connect to a different host:

```bash
python client.py --host 192.168.1.10:50051 https://example.com
```
