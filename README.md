# Nightingale Webhook

夜莺（Nightingale）监控告警 → 阿里云语音通知 Webhook 服务。

当夜莺监控系统触发告警时，通过 HTTP webhook 调用本服务，自动拨打值班人员电话通知。

## 功能

- **告警接收**：接收夜莺 POST JSON 告警事件
- **智能过滤**：按严重等级、是否恢复自动筛选
- **去重限流**：告警级 hash 去重 + 业务组级时间窗口限流
- **值班轮换**：按日期 + 业务组匹配值班人员，支持 CSV 导入/导出/在线编辑
- **语音呼叫**：调用阿里云 Dyvmsapi `SingleCallByTts` 拨打电话
- **呼叫重试**：指数退避重试（1s → 2s → 4s），最多 3 次
- **结果回查**：后台 poller 在 30s/60s/120s 查询呼叫结果
- **并发控制**：可配置最大并发呼叫数
- **监控面板**：内置 Web 面板，实时查看呼叫历史、统计、限流状态
- **双平台构建**：支持 macOS ARM + Linux AMD64 静态编译

## 快速开始

### 前置准备

1. **阿里云 VMS**：开通语音服务，申请显号，创建 TTS 模板，获取 AccessKey
2. **夜莺配置**：添加回调通知媒介，URL 指向 `http://<本服务>:8080/api/v1/nightingale/webhook`

### 安装运行

```bash
# 编译
make build
# 或交叉编译
make build-all

# 配置
cp config.example.yaml config.yaml
# 编辑 config.yaml 填入阿里云密钥和业务组信息

# dry run 模式测试（不实际拨打电话）
make run

# 生产模式
./build/webhook -config config.yaml
```

### 配置示例

```yaml
server:
  port: 8080

aliyun:
  access_key_id: "your-key"
  access_key_secret: "your-secret"
  called_show_number: "0571XXXXXXXX"
  tts_code: "TTS_XXXXXXXXXX"
  play_times: 2

alert:
  min_severity: 2
  cooldown: 15m
  group_cooldown: 5m
  max_concurrent_calls: 5

server_groups:
  "运维组":
    phones:
      - "13800001111"
      - "13800002222"

always_phones:
  - "13600000000"

logging:
  level: "info"
  format: "json"
  output: "data/webhook.log"   # 留空仅输出到 stdout
  max_size: 10
  max_age: 7
  max_backups: 5
```

### 测试

```bash
# 单元测试
make test

# 手动发送测试告警
curl -X POST http://localhost:8080/api/v1/nightingale/webhook \
  -H "Content-Type: application/json" \
  -d '[{
    "id": 1,
    "rule_name": "CPU过高",
    "severity": 1,
    "is_recovered": false,
    "trigger_value": "95.5",
    "hash": "abc123",
    "group_name": "运维组",
    "tags_map": {},
    "annotations": {}
  }]'
```

## API

| 端点 | 说明 |
|---|---|
| `POST /api/v1/nightingale/webhook` | 接收夜莺告警 |
| `GET /api/v1/health` | 健康检查 |
| `GET /api/v1/calls?limit=50` | 呼叫历史 |
| `GET /api/v1/stats` | 今日统计 |
| `GET /api/v1/cooldowns` | 限流状态 |
| `POST /api/v1/schedule/import` | 上传值班表 CSV |
| `GET /api/v1/schedule/today` | 今日值班 |
| `PUT /api/v1/schedule/entry` | 编辑排班 |
| `DELETE /api/v1/schedule/entry` | 删除排班 |
| `GET /api/v1/schedule/export?month=2026-06` | 导出值班表 |
| `GET /api/v1/schedule/changes?limit=50` | 编辑日志 |
| `GET /` | 监控面板 |

## 项目结构

```
.
├── cmd/webhook/main.go           # 入口
├── internal/
│   ├── alert/event.go            # 告警模型 + 电话匹配
│   ├── config/config.go          # 配置加载
│   ├── store/                    # SQLite 持久化
│   ├── notifier/                 # 阿里云语音呼叫 + 重试 + 回查
│   ├── schedule/                 # 值班表 CSV 解析
│   ├── webhook/handler.go        # 核心处理器
│   └── api/                      # HTTP 服务器 + 路由 + 面板
├── config.example.yaml           # 示例配置
├── Makefile                      # 构建脚本
└── web/                          # 前端静态文件
```

## 构建

```bash
make build-all
# → build/webhook-darwin-arm64  (macOS Apple Silicon)
# → build/webhook-linux-amd64   (Linux x86_64)
```

全部静态链接（`CGO_ENABLED=0`），Linux 上可直接运行。

## License

MIT
