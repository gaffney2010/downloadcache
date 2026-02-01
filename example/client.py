"""Example Python client for the DownloadCache gRPC service."""

import argparse
import grpc

import downloadcache_pb2
import downloadcache_pb2_grpc


def run(host: str, url: str, invalidate: bool) -> None:
    with grpc.insecure_channel(host) as channel:
        stub = downloadcache_pb2_grpc.DownloadCacheStub(channel)

        request = downloadcache_pb2.DownloadCacheRequest(
            url=url,
            invalidate=invalidate,
        )

        print(f"Requesting: {url} (invalidate={invalidate})")
        response = stub.Get(request)

        print(f"Response length: {len(response.page_contents)} chars")
        print("---")
        print(response.page_contents[:2000])
        if len(response.page_contents) > 2000:
            print(f"\n... (truncated, {len(response.page_contents) - 2000} more chars)")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="DownloadCache gRPC client")
    parser.add_argument(
        "--host",
        default="localhost:50051",
        help="gRPC server address (default: localhost:50051)",
    )
    parser.add_argument(
        "url",
        help="URL to fetch through the cache",
    )
    parser.add_argument(
        "--invalidate",
        action="store_true",
        help="Force re-download, bypassing the cache",
    )
    args = parser.parse_args()

    run(args.host, args.url, args.invalidate)
