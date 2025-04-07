package logger

import (
	"fmt"
	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
	"os"
	"path/filepath"
	"time"
)

// Log 全局日志实例
var Log *logrus.Logger

// Init 初始化日志配置
func Init() {
	Log = logrus.New()

	// 获取日志文件路径，优先从环境变量中获取，若没有则使用默认路径
	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "logs/application.log" // 默认日志文件路径
	}

	// 创建日志文件目录
	err := os.MkdirAll(filepath.Dir(logFilePath), 0755)
	if err != nil {
		fmt.Printf("Failed to create log directory: %v\n", err)
		os.Exit(1)
	}

	// 设置日志轮转
	Log.SetOutput(&lumberjack.Logger{
		Filename:   logFilePath, // 日志文件路径
		MaxSize:    10,          // 每个日志文件的最大大小（MB）
		MaxBackups: 3,           // 保留3个备份日志文件
		MaxAge:     28,          // 日志文件保留28天
		Compress:   true,        // 启用日志压缩
	})

	// 设置日志格式
	Log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,                  // 启用完整时间戳
		TimestampFormat: "2006-01-02 15:04:05", // 时间格式化
	})

	// 设置日志级别，默认 DebugLevel
	Log.SetLevel(logrus.DebugLevel)

	// 使用 Hook 让 logrus 使用 UTC+8
	Log.AddHook(&localTimeHook{})
}

// localTimeHook 自定义 Hook，强制转换日志时间为北京时间
type localTimeHook struct{}

func (h *localTimeHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *localTimeHook) Fire(entry *logrus.Entry) error {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 兜底方案：如果无法加载 "Asia/Shanghai"，使用固定时区
		loc = time.FixedZone("CST", 8*60*60) // CST (China Standard Time) UTC+8
	}
	entry.Time = entry.Time.In(loc)
	return nil
}

// LogError 记录错误级别日志
func LogError(format string, args ...interface{}) {
	Log.Errorf(format, args...)
}

// LogInfo 记录信息级别日志
func LogInfo(format string, args ...interface{}) {
	Log.Infof(format, args...)
}

// LogDebug 记录调试级别日志
func LogDebug(format string, args ...interface{}) {
	Log.Debugf(format, args...)
}
