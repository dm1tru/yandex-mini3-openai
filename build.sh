#!/bin/sh
set -eu
cd "$(dirname "$0")"
docker build --output type=local,dest=dist .
echo 'Build complete: dist/'
