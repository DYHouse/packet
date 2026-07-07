package logger

import (
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	log  *zap.SugaredLogger
	once sync.Once
)

// LogConfig 日志配置
type LogConfig struct {
	Level    string
	Filename string
}

// Init 初始化日志
func Init(cfg *LogConfig) {
	once.Do(func() {
		level := parseLevel(cfg.Level)

		encoderConfig := zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}

		// Console output
		consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
		consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level)

		cores := []zapcore.Core{consoleCore}

		// File output (optional)
		if cfg.Filename != "" {
			file, err := os.OpenFile(cfg.Filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)
				fileCore := zapcore.NewCore(jsonEncoder, zapcore.AddSync(file), level)
				cores = append(cores, fileCore)
			}
		}

		core := zapcore.NewTee(cores...)
		zapLog := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel))
		log = zapLog.Sugar()
	})
}

func parseLevel(levelStr string) zapcore.Level {
	switch levelStr {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// L 获取日志实例
func L() *zap.SugaredLogger {
	if log == nil {
		Init(&LogConfig{Level: "info"})
	}
	return log
}

func Info(msg string, keysAndValues ...interface{})  { L().Infow(msg, keysAndValues...) }
func Debug(msg string, keysAndValues ...interface{}) { L().Debugw(msg, keysAndValues...) }
func Warn(msg string, keysAndValues ...interface{})  { L().Warnw(msg, keysAndValues...) }
func Error(msg string, keysAndValues ...interface{}) { L().Errorw(msg, keysAndValues...) }
func Fatal(msg string, keysAndValues ...interface{}) { L().Fatalw(msg, keysAndValues...) }

// Sync 同步日志缓冲
func Sync() {
	if log != nil {
		_ = log.Sync()
	}
}
