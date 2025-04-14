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

	// 使用 Hook 让 logrus 使用北京时间
	Log.AddHook(NewLocalTimeHook())
}

// LocalTimeHook 自定义 Hook，强制转换日志时间为北京时间
type LocalTimeHook struct {
	loc *time.Location
}

// NewLocalTimeHook 创建并返回一个新的 LocalTimeHook 实例
func NewLocalTimeHook() *LocalTimeHook {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 使用固定时区
		loc = time.FixedZone("CST", 8*60*60)
	}
	return &LocalTimeHook{loc: loc}
}

func (h *LocalTimeHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *LocalTimeHook) Fire(entry *logrus.Entry) error {
	// 使用缓存的时区进行时间转换
	entry.Time = entry.Time.In(h.loc)
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

// asyncLogWriter 异步日志记录函数
func asyncLogWriter(entry *logrus.Entry) {
	// 将日志记录操作放入 goroutine 中
	go func() {
		Log.WithFields(entry.Data).Log(entry.Level, entry.Message)
	}()
}

// LogErrorAsync 异步记录错误级别日志
func LogErrorAsync(format string, args ...interface{}) {
	go Log.Errorf(format, args...)
}

// LogInfoAsync 异步记录信息级别日志
func LogInfoAsync(format string, args ...interface{}) {
	go Log.Infof(format, args...)
}

// LogDebugAsync 异步记录调试级别日志
func LogDebugAsync(format string, args ...interface{}) {
	go Log.Debugf(format, args...)
}
