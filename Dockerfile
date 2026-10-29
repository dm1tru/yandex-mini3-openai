FROM golang:1.25 AS go-build
WORKDIR /src
COPY mini3-openai/ ./
RUN go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/mini3-openai .

FROM golang:1.25 AS vosk-download
COPY buildtools/fetch-vosk.go /fetch-vosk.go
RUN go run /fetch-vosk.go

FROM debian:bullseye-slim AS c-build
RUN apt-get update && apt-get install -y --no-install-recommends gcc-arm-linux-gnueabihf libc6-dev-armhf-cross && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY --from=vosk-download /vosk/ ./vosk-linux-armv7l-0.3.45/
COPY vosk-stream.c vosk-transcribe.c ./
RUN mkdir -p /out && for program in vosk-stream vosk-transcribe; do arm-linux-gnueabihf-gcc -O2 -Wall -Wextra -pthread -Ivosk-linux-armv7l-0.3.45 "$program.c" -Lvosk-linux-armv7l-0.3.45 -Wl,--allow-shlib-undefined -Wl,-rpath,/data/opt/vosk/lib -lvosk -lm -o "/out/$program"; done && cp vosk-linux-armv7l-0.3.45/libvosk.so /out/

FROM scratch
COPY --from=go-build /out/ /
COPY --from=c-build /out/ /
