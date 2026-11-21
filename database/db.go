package database

import (
	"fmt"
	"log"

	"video-feed/config"
	"video-feed/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 全局数据库句柄
var DB *gorm.DB

// InitMySQL 连接 MySQL 并自动建表
func InitMySQL(cfg *config.MySQLConfig) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Info), // 打印 SQL，学习期开着
		TranslateError: true,                                // 把 MySQL 1062 翻译成 gorm.ErrDuplicatedKey
	})
	if err != nil {
		return fmt.Errorf("连接 MySQL 失败: %w", err)
	}

	DB = db
	log.Println("✅ MySQL 连接成功:", cfg.DBName)

	// 自动建表（users / follows / videos / likes / comments）
	if err := db.AutoMigrate(
		&models.User{},
		&models.Follow{},
		&models.Video{},
		&models.Like{},
		&models.Comment{},
	); err != nil {
		return fmt.Errorf("自动建表失败: %w", err)
	}
	log.Println("✅ 数据表自动迁移完成")
	return nil
}
