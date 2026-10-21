package config

import "os"

// Config 全局配置，从环境变量加载，未设置时使用本地开发默认值
type Config struct {
	Server ServerConfig
	MySQL  MySQLConfig
	Redis  RedisConfig
	JWT    JWTConfig
	LLM    LLMConfig // 第二阶段接入大模型时用，先占位
}

type ServerConfig struct {
	Host string
	Port string
}

type MySQLConfig struct {
	User     string
	Password string
	Host     string
	Port     string
	DBName   string
}

type RedisConfig struct {
	Host string
	Port string
}

type JWTConfig struct {
	Secret string
	Expire int // 过期小时数
}

type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Load 读取环境变量，为空时返回默认值
func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Host: getEnv("VIDEO_SERVER_HOST", "0.0.0.0"),
			Port: getEnv("VIDEO_SERVER_PORT", "8081"),
		},
		MySQL: MySQLConfig{
			User:     getEnv("VIDEO_DB_USER", "root"),
			Password: getEnv("VIDEO_DB_PASSWORD", "123456"),
			Host:     getEnv("VIDEO_DB_HOST", "127.0.0.1"),
			Port:     getEnv("VIDEO_DB_PORT", "3306"),
			DBName:   getEnv("VIDEO_DB_NAME", "video_feed"),
		},
		Redis: RedisConfig{
			Host: getEnv("VIDEO_REDIS_HOST", "127.0.0.1"),
			Port: getEnv("VIDEO_REDIS_PORT", "6379"),
		},
		JWT: JWTConfig{
			Secret: getEnv("VIDEO_JWT_SECRET", "video-feed-secret"),
			Expire: 24,
		},
		LLM: LLMConfig{
			BaseURL: getEnv("VIDEO_LLM_BASE_URL", "https://api.deepseek.com/v1"),
			APIKey:  getEnv("VIDEO_LLM_API_KEY", ""),
			Model:   getEnv("VIDEO_LLM_MODEL", "deepseek-chat"),
		},
	}
}

// getEnv 读取环境变量，为空时返回默认值
func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
