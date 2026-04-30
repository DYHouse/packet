package mysql

import (
	"fmt"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// NewDB 创建MySQL连接
func NewDB(cfg *config.MySQLConfig) (*gorm.DB, error) {
	gormConfig := &gorm.Config{
		Logger: NewCustomLogger(),
	}

	db, err := gorm.Open(mysql.Open(cfg.DSN), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mysql: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	// Ping测试
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping mysql: %w", err)
	}

	logger.Info("mysql connected", "dsn", maskDSN(cfg.DSN))
	return db, nil
}

// maskDSN 隐藏DSN中的密码
func maskDSN(dsn string) string {
	// 简单隐藏密码
	if len(dsn) > 20 {
		return dsn[:10] + "***" + dsn[len(dsn)-10:]
	}
	return "***"
}
