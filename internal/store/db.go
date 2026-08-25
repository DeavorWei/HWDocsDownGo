package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"hwdocsdown/internal/logger"
)

var DB *gorm.DB

// InitDB 初始化纯 Go SQLite 数据库
func InitDB(dbPath string) (*gorm.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 配置 DSN 启用 WAL 模式与 10000ms 忙等待超时，解决并发写锁冲突
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)", dbPath)

	// 将 GORM 日志设为 Silent，彻底屏蔽超长 Slow SQL 刷屏
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 数据库失败: %w", err)
	}

	// 限制连接池：SQLite 单文件写入机制下设置单写连接池，防止写锁并发争用
	sqlDB, err := db.DB()
	if err == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}

	// 自动迁移所有模型
	err = db.AutoMigrate(
		&Category{},
		&ProductLine{},
		&Product{},
		&SubModel{},
		&Version{},
		&Document{},
		&DownloadTask{},
		&Setting{},
	)
	if err != nil {
		return nil, fmt.Errorf("数据库表迁移失败: %w", err)
	}

	DB = db
	logger.Info("SQLite 数据库初始化与表迁移成功", zap.String("dbPath", dbPath))
	return db, nil
}
