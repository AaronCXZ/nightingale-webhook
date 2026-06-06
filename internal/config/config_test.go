package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}
	return path
}

func TestLoadValidDryRun(t *testing.T) {
	// dry_run=true 时阿里云字段可选，加载应该成功
	path := writeTempConfig(t, `
server:
  port: 8080
alert:
  min_severity: 2
  cooldown: 15m
  group_cooldown: 5m
  max_concurrent_calls: 5
dry_run: true
storage:
  db_path: "data/webhook.db"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load 不应出错: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg 为 nil")
	}
	if !cfg.DryRun {
		t.Error("DryRun 应为 true")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Port 应为 8080，got %d", cfg.Server.Port)
	}
}

func TestLoadValidFullConfig(t *testing.T) {
	// 完整配置，验证默认值被正确应用
	path := writeTempConfig(t, `
dry_run: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load 不应出错: %v", err)
	}
	if cfg.Server.Port != defaultPort {
		t.Errorf("Port 默认值应为 %d，got %d", defaultPort, cfg.Server.Port)
	}
	if cfg.Alert.MinSeverity != defaultMinSeverity {
		t.Errorf("MinSeverity 默认值应为 %d，got %d", defaultMinSeverity, cfg.Alert.MinSeverity)
	}
	if cfg.Alert.Cooldown != defaultCooldown {
		t.Errorf("Cooldown 默认值应为 %v，got %v", defaultCooldown, cfg.Alert.Cooldown)
	}
	if cfg.Alert.GroupCooldown != defaultGroupCooldown {
		t.Errorf("GroupCooldown 默认值应为 %v，got %v", defaultGroupCooldown, cfg.Alert.GroupCooldown)
	}
	if cfg.Alert.MaxConcurrentCalls != defaultMaxConcurrentCalls {
		t.Errorf("MaxConcurrentCalls 默认值应为 %d，got %d", defaultMaxConcurrentCalls, cfg.Alert.MaxConcurrentCalls)
	}
	if cfg.Retry.MaxAttempts != defaultMaxAttempts {
		t.Errorf("Retry.MaxAttempts 默认值应为 %d，got %d", defaultMaxAttempts, cfg.Retry.MaxAttempts)
	}
	if cfg.Retry.BackoffBase != defaultBackoffBase {
		t.Errorf("Retry.BackoffBase 默认值应为 %v，got %v", defaultBackoffBase, cfg.Retry.BackoffBase)
	}
	if cfg.Storage.DBPath != defaultDBPath {
		t.Errorf("Storage.DBPath 默认值应为 %s，got %s", defaultDBPath, cfg.Storage.DBPath)
	}
	if cfg.Aliyun.PlayTimes != defaultPlayTimes {
		t.Errorf("Aliyun.PlayTimes 默认值应为 %d，got %d", defaultPlayTimes, cfg.Aliyun.PlayTimes)
	}
	if cfg.Logging.Level != defaultLogLevel {
		t.Errorf("Logging.Level 默认值应为 %s，got %s", defaultLogLevel, cfg.Logging.Level)
	}
	if cfg.Logging.Format != defaultLogFormat {
		t.Errorf("Logging.Format 默认值应为 %s，got %s", defaultLogFormat, cfg.Logging.Format)
	}
	if cfg.Logging.MaxSize != defaultLogMaxSize {
		t.Errorf("Logging.MaxSize 默认值应为 %d，got %d", defaultLogMaxSize, cfg.Logging.MaxSize)
	}
	if cfg.Logging.MaxAge != defaultLogMaxAge {
		t.Errorf("Logging.MaxAge 默认值应为 %d，got %d", defaultLogMaxAge, cfg.Logging.MaxAge)
	}
	if cfg.Logging.MaxBackups != defaultLogMaxBackups {
		t.Errorf("Logging.MaxBackups 默认值应为 %d，got %d", defaultLogMaxBackups, cfg.Logging.MaxBackups)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/tmp/nonexistent_config_" + filepath.Base(t.Name()) + ".yaml")
	if err == nil {
		t.Error("文件不存在时应返回错误")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTempConfig(t, `invalid: yaml: [bad`)
	_, err := Load(path)
	if err == nil {
		t.Error("YAML 格式错误时应返回错误")
	}
}

func TestLoadNoDryRunMissingAliyun(t *testing.T) {
	// dry_run=false 但缺少阿里云字段，应报错
	path := writeTempConfig(t, `
server:
  port: 8080
dry_run: false
`)

	_, err := Load(path)
	if err == nil {
		t.Error("dry_run=false 且缺少阿里云字段时应返回错误")
	}
}

func TestLoadInvalidPort(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: 99999
dry_run: true
`)
	_, err := Load(path)
	if err == nil {
		t.Error("port 超出范围时应返回错误")
	}
}

func TestLoadPortZero(t *testing.T) {
	// port=0 应该被默认值覆盖，所以成功
	path := writeTempConfig(t, `
server:
  port: 0
dry_run: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("port=0 应被默认值覆盖，不应报错: %v", err)
	}
	if cfg.Server.Port != defaultPort {
		t.Errorf("Port 应为默认值 %d，got %d", defaultPort, cfg.Server.Port)
	}
}

func TestLoadInvalidSeverity(t *testing.T) {
	// min_severity=0 被 applyDefaults 替换为默认值 2，不会报错
	path := writeTempConfig(t, `
alert:
  min_severity: 0
dry_run: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("min_severity=0 应被默认值覆盖，不应报错: %v", err)
	}
	if cfg.Alert.MinSeverity != defaultMinSeverity {
		t.Errorf("MinSeverity 应为默认值 %d，got %d", defaultMinSeverity, cfg.Alert.MinSeverity)
	}

	// min_severity=4 超出 1-3 范围，应报错
	path2 := writeTempConfig(t, `
alert:
  min_severity: 4
dry_run: true
`)
	_, err = Load(path2)
	if err == nil {
		t.Error("min_severity=4 超出 1-3 范围时应返回错误")
	}
}

func TestApplyDefaults(t *testing.T) {
	// 直接测试 applyDefaults 行为
	cfg := &Config{}
	cfg.applyDefaults()

	if cfg.Server.Port != defaultPort {
		t.Errorf("Port 默认值应为 %d", defaultPort)
	}
	if cfg.Alert.MinSeverity != defaultMinSeverity {
		t.Errorf("MinSeverity 默认值应为 %d", defaultMinSeverity)
	}
	if cfg.Alert.Cooldown != defaultCooldown {
		t.Errorf("Cooldown 默认值应为 %v", defaultCooldown)
	}
	if cfg.Alert.MaxConcurrentCalls != defaultMaxConcurrentCalls {
		t.Errorf("MaxConcurrentCalls 默认值应为 %d", defaultMaxConcurrentCalls)
	}
	if cfg.Retry.MaxAttempts != defaultMaxAttempts {
		t.Errorf("Retry.MaxAttempts 默认值应为 %d", defaultMaxAttempts)
	}
	if cfg.Storage.DBPath != defaultDBPath {
		t.Errorf("Storage.DBPath 默认值应为 %s", defaultDBPath)
	}
	if cfg.Aliyun.PlayTimes != defaultPlayTimes {
		t.Errorf("Aliyun.PlayTimes 默认值应为 %d", defaultPlayTimes)
	}
	if cfg.Logging.Level != defaultLogLevel {
		t.Errorf("Logging.Level 默认值应为 %s", defaultLogLevel)
	}
}

func TestIsDryRun(t *testing.T) {
	cfg := &Config{DryRun: true}
	if !cfg.IsDryRun() {
		t.Error("DryRun=true 时应返回 true")
	}

	cfg.DryRun = false
	if cfg.IsDryRun() {
		t.Error("DryRun=false 时应返回 false")
	}
}

func TestLoadRetentionDefault(t *testing.T) {
	path := writeTempConfig(t, `dry_run: true`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load 不应出错: %v", err)
	}
	if cfg.Storage.CallRetention != defaultCallRetention {
		t.Errorf("CallRetention 默认值应为 %v，got %v", defaultCallRetention, cfg.Storage.CallRetention)
	}
}
