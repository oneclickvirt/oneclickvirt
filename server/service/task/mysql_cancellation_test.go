package task

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
)

// Use a disposable local database with an ocv_local_ name. This test never
// reads the controller's config.yaml or connects to provider nodes.
func TestLocalMySQLCompletionSerializesCancellation(t *testing.T) {
	dsn := os.Getenv("OCV_LOCAL_MYSQL_DSN")
	if dsn == "" && os.Getenv("OCV_LOCAL_MYSQL_ADDR") != "" {
		fixture := mysqlDriver.NewConfig()
		fixture.Net = "tcp"
		fixture.Addr = os.Getenv("OCV_LOCAL_MYSQL_ADDR")
		fixture.User = "oneclickvirt"
		fixture.Passwd = os.Getenv("OCV_LOCAL_MYSQL_PASSWORD")
		fixture.DBName = "ocv_local_task_cancel"
		dsn = fixture.FormatDSN()
	}
	if dsn == "" {
		t.Skip("requires isolated loopback MySQL fixture")
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid local fixture DSN")
	}
	if !strings.HasPrefix(cfg.DBName, "ocv_local_") || (cfg.Net != "unix" && !strings.HasPrefix(cfg.Addr, "127.0.0.1:")) {
		t.Fatal("refusing nonlocal fixture")
	}
	cfg.ParseTime = true
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	dsn = cfg.FormatDSN()
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{IgnoreRelationshipsWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if db.Migrator().HasTable(&adminModel.Task{}) {
		t.Fatal("fixture database must not contain a tasks table")
	}
	if err := db.AutoMigrate(&adminModel.Task{}); err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	oldDB, oldLog, oldScheduler := global.APP_DB, global.APP_LOG, global.APP_SCHEDULER
	global.APP_DB, global.APP_LOG, global.APP_SCHEDULER = db, zap.NewNop(), nil
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&adminModel.Task{})
		_ = sqlDB.Close()
		global.APP_DB, global.APP_LOG, global.APP_SCHEDULER = oldDB, oldLog, oldScheduler
	})
	row := adminModel.Task{Status: "running", TaskType: "fixture", TaskData: `{}`}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	releaseCompletion := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseCompletion()
	if err := db.Callback().Query().After("gorm:query").Register("test:hold_completion", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" && strings.Contains(tx.Statement.SQL.String(), "FOR UPDATE") {
			enterOnce.Do(func() { close(entered) })
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("test:hold_completion")
	svc := newCancellationTaskService()
	done := make(chan error, 1)
	go func() { done <- svc.CompleteTask(row.ID, true, "", nil) }()
	completionFinished := false
	awaitCompletion := func() error {
		select {
		case err := <-done:
			completionFinished = true
			return err
		case <-time.After(8 * time.Second):
			return errors.New("completion fixture did not terminate")
		}
	}
	// Release and join before restoring globals or dropping the fixture table,
	// including when a preceding assertion fails.
	t.Cleanup(func() {
		releaseCompletion()
		if !completionFinished {
			if err := awaitCompletion(); err != nil {
				t.Error(err)
			}
		}
	})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		releaseCompletion()
		t.Fatal("completion did not lock row")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "SET SESSION innodb_lock_wait_timeout = 1")
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := conn.ExecContext(ctx, "UPDATE tasks SET status='cancelling' WHERE id=? AND status='running'", row.ID)
	releaseCompletion()
	completeErr := awaitCompletion()
	if completeErr != nil {
		t.Fatal(completeErr)
	}
	var mysqlErr *mysqlDriver.MySQLError
	if !errors.As(updateErr, &mysqlErr) || mysqlErr.Number != 1205 {
		t.Fatalf("cancellation bypassed completion lock: %v", updateErr)
	}
	if err := db.First(&row, row.ID).Error; err != nil || row.Status != "completed" {
		t.Fatalf("unexpected terminal state %s: %v", row.Status, err)
	}
}
