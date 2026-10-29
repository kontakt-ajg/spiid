#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
root=$PWD
go=${GO:-go}
mkdir -p dist
for target in darwin:arm64:macos darwin:amd64:macos linux:amd64:linux linux:arm64:linux windows:amd64:windows windows:arm64:windows; do
 IFS=: read -r os arch label <<< "$target"
 suffix=; [[ $os != windows ]] || suffix=.exe
 (CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$go" build -trimpath -ldflags "-s -w" -o "$root/dist/spiid-$label-$arch$suffix" ./cmd/spiid)
done
du -sh dist/spiid-*
