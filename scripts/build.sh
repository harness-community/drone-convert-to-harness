#!/bin/sh

# Build for multiple platforms
set -e
set -x

# Create release directory
mkdir -p release/linux/amd64
mkdir -p release/linux/arm64
mkdir -p release/windows/amd64

# Force Go modules
export GO111MODULE=on

# Clone go-convert repository (CI-17808 branch)
echo "Cloning go-convert repository (CI-17808 branch)..."
rm -rf temp-go-convert || true
git clone https://github.com/drone/go-convert/ -b CI-17808 temp-go-convert
cd temp-go-convert

# Build go-convert binaries for multiple platforms
echo "Building go-convert binaries..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ../release/linux/amd64/go-convert
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ../release/linux/arm64/go-convert
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ../release/windows/amd64/go-convert.exe

# Return to the original directory
cd ..

# Build drone-convert-to-harness binaries
echo "Building drone-convert-to-harness binaries..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o release/linux/amd64/drone-convert-to-harness
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o release/linux/arm64/drone-convert-to-harness
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o release/windows/amd64/drone-convert-to-harness.exe

# Clean up temporary directory
rm -rf temp-go-convert
