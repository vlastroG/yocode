FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/yocode ./cmd/yocode \
 && CGO_ENABLED=0 go build -o /out/yocode-mcp ./cmd/yocode-mcp

FROM alpine:3.20
# The project is bind-mounted from Windows: trust any owner and normalize line endings and file modes,
# so `git status` doesn't report every file as modified.
RUN apk add --no-cache git ca-certificates \
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
