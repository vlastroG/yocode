# Debian (glibc) rather than Alpine: Docker Desktop's DNS returns malformed AAAA answers that
# musl's resolver treats as failures, which breaks apk/go downloads during the build.
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/yocode ./cmd/yocode \
 && CGO_ENABLED=0 go build -o /out/yocode-mcp ./cmd/yocode-mcp

# Integration tests (DeepSeek chat, local embeddings): docker compose run --rm itest
FROM build AS itest
RUN apt-get update \
 && apt-get install -y --no-install-recommends git \
 && rm -rf /var/lib/apt/lists/* \
 && git config --system --add safe.directory '*' \
 && git config --system core.autocrlf true \
 && git config --system core.fileMode false
ENTRYPOINT ["go", "test", "-tags", "integration", "-v", "-timeout", "90m", "./internal/integration/"]

FROM debian:bookworm-slim
# The project is bind-mounted from Windows: trust any owner and normalize line endings and file modes,
# so `git status` doesn't report every file as modified.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && git config --system --add safe.directory '*' \
 && git config --system core.autocrlf true \
 && git config --system core.fileMode false
COPY --from=build /out/ /usr/local/bin/
ENV PROJECT_DIR=/project \
    DOCS_DIR=/docs \
    DATA_DIR=/data \
    OLLAMA_URL=http://ollama:11434 \
    YOCODE_MCP=yocode-mcp \
    TERM=xterm-256color
WORKDIR /project
ENTRYPOINT ["yocode"]
