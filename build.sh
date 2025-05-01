#!/bin/bash
# filepath: /Users/mt29/Documents/ws/tmp/aws-tag-analysis/build.sh

# Exit immediately if a command exits with a non-zero status.
set -e

# Define the output binary name
OUTPUT_NAME="aws-tag-analyzer"

# Define the main package path
MAIN_PACKAGE="./cmd/main.go"

# Define ldflags for stripping symbols and debug info
LDFLAGS="-s -w"

echo "Building ${OUTPUT_NAME}..."

# Build the Go application statically linked and stripped
# CGO_ENABLED=0 disables Cgo, which is necessary for static builds without external C dependencies.
# -ldflags="${LDFLAGS}" applies the stripping flags.
CGO_ENABLED=0 go build -ldflags="${LDFLAGS}" -o "${OUTPUT_NAME}" "${MAIN_PACKAGE}"

echo "Build complete: ./${OUTPUT_NAME}"

# Optional: Show file size and type
ls -lh "${OUTPUT_NAME}"
file "${OUTPUT_NAME}"