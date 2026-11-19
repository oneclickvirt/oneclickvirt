package snapshot

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A real MySQL or MariaDB transaction is needed to exercise the read view that
// a waiting request sees after it acquires the instance row lock.
func TestLocalMySQLSnapshotQuotaSerializesReservations(t *testing.T) {
	dsn := os.Getenv("OCV_SNAPSHOT_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires an isolated loopback MySQL fixture")
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "ocv_local_") ||
		(cfg.Net != "unix" && !strings.HasPrefix(cfg.Addr, "127.0.0.1:")) {
		t.Fatal("invalid local fixture DSN")
	}
	cfg.ParseTime = true
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 10*time.Second, 20*time.Second, 20*time.Second
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{
		IgnoreRelationshipsWhenMigrating: true,
		Logger:                           logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(10)
	tables := []any{&providerModel.Instance{}, &userModel.User{}, &providerModel.InstanceSnapshot{},
		&providerModel.SnapshotTask{}, &adminModel.Task{}, &adminModel.SystemConfig{}}
	for _, table := range tables {
		if db.Migrator().HasTable(table) {
			t.Fatalf("fixture database already contains %T", table)
		}
	}
	if err := db.AutoMigrate(tables...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Migrator().DropTable(tables...) })
	oldDB, oldScheduler := global.APP_DB, global.APP_SCHEDULER
	global.APP_DB, global.APP_SCHEDULER = db, nil
	t.Cleanup(func() { global.APP_DB, global.APP_SCHEDULER = oldDB, oldScheduler })

	owner := userModel.User{Username: "snapshot-fixture", Email: "snapshot@example.invalid", Level: 1}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name   string
		userID uint
		quota  int
	}{
		{name: "unassigned", quota: 3},
		{name: "owned", userID: owner.ID, quota: 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for round := range 3 {
				inst := providerModel.Instance{
					Name: fmt.Sprintf("%s-%d", fixture.name, round), ProviderID: 1,
					UserID: fixture.userID, InstanceType: "container", Status: "running",
				}
				if err := db.Create(&inst).Error; err != nil {
					t.Fatal(err)
				}
				for i := 1; i < fixture.quota; i++ {
					snapshot := providerModel.InstanceSnapshot{
						InstanceID: inst.ID, Name: fmt.Sprintf("previous-%d", i), Status: StatusAvailable,
					}
					if err := db.Create(&snapshot).Error; err != nil {
						t.Fatal(err)
					}
				}
				const requests = 8
				start := make(chan struct{})
				results := make(chan error, requests)
				for i := range requests {
					go func() {
						<-start
						_, err := (&Service{}).startCreateSnapshotTaskPrepared(&inst, SnapshotRequest{
							Name: fmt.Sprintf("parallel-%d", i),
						}, 1, "manual", nil, "incus")
						results <- err
					}()
				}
				close(start)

				accepted, rejected := 0, 0
				for range requests {
					err := <-results
					var appErr *common.AppError
					switch {
					case err == nil:
						accepted++
					case errors.As(err, &appErr) && appErr.Code == common.CodeConflict:
						rejected++
					default:
						t.Errorf("snapshot admission returned %v", err)
					}
				}
				var snapshotCount, taskCount int64
				if err := db.Model(&providerModel.InstanceSnapshot{}).Where("instance_id = ?", inst.ID).Count(&snapshotCount).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&adminModel.Task{}).Where("instance_id = ?", inst.ID).Count(&taskCount).Error; err != nil {
					t.Fatal(err)
				}
				if accepted != 1 || rejected != requests-1 || snapshotCount != int64(fixture.quota) || taskCount != 1 {
					t.Fatalf("round %d: %d accepted, %d rejected, %d snapshots and %d tasks", round, accepted, rejected, snapshotCount, taskCount)
				}
			}
		})
	}
}
