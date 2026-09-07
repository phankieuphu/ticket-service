package database_provider

import (
	"fmt"
	"ticket-service/config"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func NewMySQLClient(cfg config.Config) (*gorm.DB, error) {
	dbCfg := cfg.Database
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&loc=UTC",
		dbCfg.Username,
		dbCfg.Password,
		dbCfg.Host,
		dbCfg.Port,
		dbCfg.Database,
	)
	fmt.Println("Connecting to MySQL with DSN:", dsn)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		PrepareStmt: true, // important for performance
	})
	if err != nil {
		return nil, err
	}

	// Configure underlying *sql.DB
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	return db, nil
}
