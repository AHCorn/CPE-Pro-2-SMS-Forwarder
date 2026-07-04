BINARY=cpe-sms-forwarder
BUILD_DIR=build
CMD=./cmd/cpe-sms-forwarder

# 版本号取自 git tag（git describe），可被外部覆盖：make build VERSION=x。
# 无 tag 时回退到短提交哈希(--always)，非 git 环境回退 dev；勿在源码里手写版本。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS=-s -w -X main.version=$(VERSION)

.PHONY: all clean build linux-amd64 linux-arm64 linux-arm linux-mipsle windows-amd64 windows-arm64 darwin-amd64 darwin-arm64 release

all: build

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) $(CMD)

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 $(CMD)

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 $(CMD)

linux-arm:
	GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-armv7 $(CMD)

linux-mipsle:
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-mipsle $(CMD)

windows-amd64:
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe $(CMD)

windows-arm64:
	GOOS=windows GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-arm64.exe $(CMD)

darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(CMD)

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 $(CMD)

# 发布资产名约定：$(BINARY)-<os>-<arch>。新增/调整平台时，scripts/install.sh 与
# scripts/install.ps1 的架构识别、release.yml 的 SHA256SUMS 与溯源 subject-path 都按此约定取文件，
# 必须同步更新，勿只改这里。
release: linux-amd64 linux-arm64 linux-arm linux-mipsle windows-amd64 windows-arm64 darwin-amd64 darwin-arm64

clean:
	rm -f $(BINARY)
	rm -rf $(BUILD_DIR)
