package mysql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/logger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type CustomLogger struct {
	gormlogger.Interface
	logLevel gormlogger.LogLevel
}

func NewCustomLogger() gormlogger.Interface {
	return &CustomLogger{
		Interface: gormlogger.Default.LogMode(gormlogger.Warn),
		logLevel:  gormlogger.Warn,
	}
}

func (l *CustomLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		l.Interface.Trace(ctx, begin, fc, err)
		return
	}

	if l.logLevel >= gormlogger.Info {
		elapsed := time.Since(begin)
		sql, rows := fc()
		logger.Debug("gorm trace",
			"elapsed", elapsed,
			"rows", rows,
			"sql", sql,
		)
	}
}

func (l *CustomLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return &CustomLogger{
		Interface: l.Interface.LogMode(level),
		logLevel:  level,
	}
}

func (l *CustomLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	logger.Info(msg, "data", fmt.Sprint(data...))
}

func (l *CustomLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	logger.Warn(msg, "data", fmt.Sprint(data...))
}

func (l *CustomLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	logger.Error(msg, "data", fmt.Sprint(data...))
}
