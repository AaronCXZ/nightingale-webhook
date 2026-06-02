package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 应用完整配置
type Config struct {
	Server        ServerConfig            `yaml:"server"`
	Aliyun        AliyunConfig            `yaml:"aliyun"`
	Alert         AlertConfig             `yaml:"alert"`
	Retry         RetryConfig             `yaml:"retry"`
	DryRun        bool                    `yaml:"dry_run"`
	Storage       StorageConfig           `yaml:"storage"`
	AlwaysPhones  []string                `yaml:"always_phones"`
	ServerGroups  map[string]ServerGroup  `yaml:"server_groups"`
	DefaultPhones []string                `yaml:"default_phones"`
	Logging       LoggingConfig           `yaml:"logging"`
}

// ServerConfig HTTP 服务器配置
type ServerConfig struct {
	Port int `yaml:"port"`
}

// AliyunConfig 阿里云语音服务配置
type AliyunConfig struct {
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	CalledShowNumber string `yaml:"called_show_number"`
	TTSCode          string `yaml:"tts_code"`
	PlayTimes        int    `yaml:"play_times"`
}

// AlertConfig 告警处理配置
type AlertConfig struct {
	MinSeverity          int           `yaml:"min_severity"`
	Cooldown             time.Duration `yaml:"cooldown"`
	GroupCooldown        time.Duration `yaml:"group_cooldown"`
	MaxConcurrentCalls   int           `yaml:"max_concurrent_calls"`
}

// RetryConfig 呼叫重试配置
type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BackoffBase time.Duration `yaml:"backoff_base"`
}

// StorageConfig 存储配置
type StorageConfig struct {
	DBPath        string        `yaml:"db_path"`
	CallRetention time.Duration `yaml:"call_retention"`
}

// ServerGroup 服务器组
type ServerGroup struct {
	Phones []string `yaml:"phones"`
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level      string `yaml:"level"`
	Format     string `yaml:"format"`
	Output     string `yaml:"output"`      // 日志文件路径，为空则仅 stdout
	MaxSize    int    `yaml:"max_size"`    // 单文件最大 MB，默认 10
	MaxAge     int    `yaml:"max_age"`     // 保留天数，默认 7
	MaxBackups int    `yaml:"max_backups"` // 最多保留旧文件数，默认 5
}

// 默认值常量
const (
	defaultPort              = 8080
	defaultMinSeverity       = 2
	defaultCooldown          = 15 * time.Minute
	defaultGroupCooldown     = 5 * time.Minute
	defaultMaxConcurrentCalls = 5
	defaultMaxAttempts       = 3
	defaultBackoffBase       = 1 * time.Second
	defaultPlayTimes         = 2
	defaultCallRetention     = 720 * time.Hour
	defaultDBPath            = "data/webhook.db"
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	defaultLogMaxSize        = 10 // MB
	defaultLogMaxAge         = 7  // days
	defaultLogMaxBackups     = 5
)

// Load 从文件路径加载配置，应用默认值，校验必填字段
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = defaultPort
	}
	if c.Alert.MinSeverity == 0 {
		c.Alert.MinSeverity = defaultMinSeverity
	}
	if c.Alert.Cooldown == 0 {
		c.Alert.Cooldown = defaultCooldown
	}
	if c.Alert.GroupCooldown == 0 {
		c.Alert.GroupCooldown = defaultGroupCooldown
	}
	if c.Alert.MaxConcurrentCalls == 0 {
		c.Alert.MaxConcurrentCalls = defaultMaxConcurrentCalls
	}
	if c.Retry.MaxAttempts == 0 {
		c.Retry.MaxAttempts = defaultMaxAttempts
	}
	if c.Retry.BackoffBase == 0 {
		c.Retry.BackoffBase = defaultBackoffBase
	}
	if c.Aliyun.PlayTimes == 0 {
		c.Aliyun.PlayTimes = defaultPlayTimes
	}
	if c.Storage.DBPath == "" {
		c.Storage.DBPath = defaultDBPath
	}
	if c.Storage.CallRetention == 0 {
		c.Storage.CallRetention = defaultCallRetention
	}
	if c.Logging.Level == "" {
		c.Logging.Level = defaultLogLevel
	}
	if c.Logging.Format == "" {
		c.Logging.Format = defaultLogFormat
	}
	if c.Logging.MaxSize == 0 {
		c.Logging.MaxSize = defaultLogMaxSize
	}
	if c.Logging.MaxAge == 0 {
		c.Logging.MaxAge = defaultLogMaxAge
	}
	if c.Logging.MaxBackups == 0 {
		c.Logging.MaxBackups = defaultLogMaxBackups
	}
}

// validate 校验必填字段（dry_run 模式下阿里云配置可选）
func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 必须在 1-65535 之间")
	}
	if c.Alert.MinSeverity < 1 || c.Alert.MinSeverity > 3 {
		return fmt.Errorf("alert.min_severity 必须在 1-3 之间")
	}

	if !c.DryRun {
		if c.Aliyun.AccessKeyID == "" {
			return fmt.Errorf("aliyun.access_key_id 不能为空（dry_run 模式下可省略）")
		}
		if c.Aliyun.AccessKeySecret == "" {
			return fmt.Errorf("aliyun.access_key_secret 不能为空（dry_run 模式下可省略）")
		}
		if c.Aliyun.CalledShowNumber == "" {
			return fmt.Errorf("aliyun.called_show_number 不能为空（dry_run 模式下可省略）")
		}
		if c.Aliyun.TTSCode == "" {
			return fmt.Errorf("aliyun.tts_code 不能为空（dry_run 模式下可省略）")
		}
	}

	return nil
}

// IsDryRun 返回是否为 dry run 模式（可被 per-request 覆盖）
func (c *Config) IsDryRun() bool {
	return c.DryRun
}
