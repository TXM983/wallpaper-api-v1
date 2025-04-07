package config

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type AppConfig struct {
	Server struct {
		Port int `mapstructure:"port"`
	} `mapstructure:"server"`

	Redis struct {
		Addr         string `mapstructure:"addr"`
		Password     string `mapstructure:"password"`
		DB           int    `mapstructure:"db"`
		PoolSize     int    `mapstructure:"pool_size"`
		MinIdleConns int    `mapstructure:"min_idle_conns"`
	} `mapstructure:"redis"`

	CDN struct {
		BaseURL string `mapstructure:"base_url"`
	} `mapstructure:"cdn"`

	OSS struct {
		Endpoint        string `mapstructure:"endpoint"`
		AccessKeyID     string `mapstructure:"access_key_id"`
		AccessKeySecret string `mapstructure:"access_key_secret"`
		Bucket          string `mapstructure:"bucket"`
	} `mapstructure:"oss"`

	INDEX struct {
		Password string `mapstructure:"password"`
	} `mapstructure:"index"`
}

// LoadConfig 加载配置文件，并支持从环境变量读取
func LoadConfig() *AppConfig {
	v := viper.New()

	// 设置配置文件的名称和类型
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// 设置默认的配置文件路径
	v.AddConfigPath("./configs")
	v.AddConfigPath("/app/configs/")

	// 尝试读取配置文件
	err := v.ReadInConfig()
	if err != nil {
		// 如果配置文件读取失败，打印日志，但不退出程序
		logrus.Warnf("Warning: unable to read config file: %s", err)
		logrus.Info("Falling back to environment variables.")
	}

	// 启用自动读取环境变量
	v.AutomaticEnv()

	// 配置环境变量与配置文件键名对应
	v.BindEnv("server.port", "SERVER_PORT")
	v.BindEnv("redis.addr", "REDIS_ADDR")
	v.BindEnv("redis.password", "REDIS_PASSWORD")
	v.BindEnv("redis.db", "REDIS_DB")
	v.BindEnv("redis.pool_size", "REDIS_POOL_SIZE")
	v.BindEnv("redis.min_idle_conns", "REDIS_MIN_IDLE_CONNS")
	v.BindEnv("cdn.base_url", "CDN_BASE_URL")
	v.BindEnv("oss.endpoint", "OSS_ENDPOINT")
	v.BindEnv("oss.access_key_id", "OSS_ACCESS_KEY_ID")
	v.BindEnv("oss.access_key_secret", "OSS_ACCESS_KEY_SECRET")
	v.BindEnv("oss.bucket", "OSS_BUCKET")
	v.BindEnv("index.password", "PASSWORD")

	// 将配置文件内容反序列化到结构体
	var cfg AppConfig
	if err := v.Unmarshal(&cfg); err != nil {
		logrus.Fatalf("unable to decode config into struct: %s", err)
	}

	logrus.Info("Loaded config successfully!")

	return &cfg
}
