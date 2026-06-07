# CLAUDE.md

Claude Code (claude.ai/code) 在此仓库工作时的指引。

## 构建、测试、运行命令

```bash
# 编译当前平台（调试用）
go build -o build/webhook ./cmd/webhook

# 交叉编译 macOS ARM + Linux AMD64（静态链接，CGO_ENABLED=0）
make build-all

# 运行全部测试
make test                            # go test ./internal/... -count=1

# 运行单个包测试
go test ./internal/store/... -count=1 -v

# 运行单个测试
go test ./internal/api/... -count=1 -v -run TestHandleHealth

# dry-run 模式运行（不会真实拨打电话）
make run                             # go run ./cmd/webhook -config config.example.yaml -dry-run

# 生产模式
./build/webhook -config config.yaml
```

## 架构概览

**夜莺告警 → 阿里云语音通知** 的桥接服务。

### 数据流

```
夜莺 → POST /api/v1/nightingale/webhook → webhook.Handler.HandleWebhook
  → 过滤：已恢复？严重等级达标？hash 去重？业务组限流？
  → resolvePhones()：从值班表查主值 + 备值号码
  → notifier.Caller.Call() → 阿里云 Dyvmsapi SingleCallByTts
  → store.SaveRecord() → SQLite
  → 后台 CallResultPoller 每 15s 回查呼叫结果
```

### 包结构

```
cmd/webhook/main.go          # 入口：初始化 config → store → caller → handler → server
internal/
  alert/event.go             # 告警模型、ResolvePhones() 号码拼装逻辑、hash 去重
  config/config.go           # YAML 配置加载，含默认值 + 校验
  store/
    models.go                # 数据模型：CallRecord, OncallPrimary/Backup, Stats 等
    store.go                 # Store 接口（所有持久化操作）
    sqlite.go                # SQLite 实现（modernc.org/sqlite，无需 CGO）
  webhook/handler.go         # 核心处理器：收告警 → 过滤 → 解析号码 → 呼叫 → 记录
  notifier/
    caller.go                # Caller 接口（Call + QueryCallStatus）
    aliyun.go                # 阿里云 Dyvmsapi 实现，指数退避重试
    mock.go                  # MockCaller（dry-run / 测试用）
    poller.go                # 后台轮询器，查阿里云呼叫结果
  schedule/
    schedule.go              # 值班表 CSV 解析 / 导出
  api/
    server.go                # HTTP 服务器、路由、所有 API handler
    middleware.go            # 日志、panic 恢复、请求 ID、CORS
    dashboard.html           # 嵌入式 HTML 监控面板（//go:embed），~880 行
```

### 关键模式

- **Store 接口** — 所有 DB 操作抽象化；生产用 SQLite（`modernc.org/sqlite`，纯 Go 无 CGO）
- **Caller 接口** — 语音呼叫抽象；dry-run 模式用 MockCaller
- **嵌入式 HTML** — `//go:embed dashboard.html` 把完整面板打进单二进制
- **无 ORM** — 裸 SQL + `database/sql`
- **全部时间戳** — UTC，`time.RFC3339` 格式

### 开发注意事项

- **Gitignore 陷阱**：`.gitignore` 中有裸 `webhook` 行，匹配根目录二进制 **和** `internal/webhook/` 目录。对已跟踪的该包文件需用 `git add -f`。
- **CGO_ENABLED=0** — 所有构建纯 Go，无 C 依赖。SQLite 用 `modernc.org/sqlite`（C → Go 转译）。
- **SQLite 单写者**：`db.SetMaxOpenConns(1)` — 该场景下安全。
- **dashboard.html 嵌入编译期**：修改 html 后需重新 `go build`。
- **配置默认值** 在 `internal/config/config.go`：端口 8080、严重等级 2+、冷却 15m 等。
