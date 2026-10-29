#!/bin/sh
set -eu
cd "$(dirname "$0")"
mkdir -p models
cd models
archive=vosk-model-small-ru-0.22.zip
curl -fL --retry 3 -o "$archive" "https://alphacephei.com/vosk/models/$archive"
echo "961d5ff98a17f4aa6de69864d0aa71fa5bac682301d2b5d17a3f24c5c99a46d4  $archive" | sha256sum -c -
unzip -q -o "$archive"
