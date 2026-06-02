APP_NAME = webhook
BUILD_DIR = build
LDFLAGS = -s -w

.PHONY: build-all build-darwin-arm64 build-linux-amd64 clean test run

# 构建全部目标平台
build-all: build-darwin-arm64 build-linux-amd64

# ARM macOS (Apple Silicon)
build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64 ./cmd/webhook

# AMD64 Linux
build-linux-amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 ./cmd/webhook

# 清理
clean:
	rm -rf $(BUILD_DIR)

# 测试
test:
	go test ./internal/... -count=1

# 运行（dry run 模式）
run:
	go run ./cmd/webhook -config config.example.yaml -dry-run

# 编译当前平台（调试用）
build:
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/webhook
