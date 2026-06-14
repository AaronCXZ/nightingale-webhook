# 会话交接

## 当前上下文

<!-- 会话结束时更新此节 -->

上次操作：
- 实现/修复内容：
- 测试结果：
- 正在进行的：

## 关键文件

| 文件 | 说明 |
|------|------|
| `AGENTS.md` | 代理入口指引 |
| `feature_list.json` | 功能清单与状态 |
| `progress.md` | 进度记录 |
| `cmd/webhook/main.go` | 入口 |
| `internal/api/dashboard.html` | 监控面板（go:embed） |

## 下次启动

```bash
# 1. 读最新状态
# 2. 确认基线
make test
# 3. 选任务做
```

## 已知问题

- `.gitignore` 中裸 `webhook` 行匹配 `internal/webhook/` 目录，对已跟踪文件需 `git add -f`
- `dashboard.html` 修改后需 `go build` 重新编译嵌入
- 网络代理限制 `git push` 到 GitHub，需要时手动执行
