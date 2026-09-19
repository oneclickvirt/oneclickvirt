package dbcompat

import (
	"sync"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestDialectIsPerConnectionAndConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for _, tc := range []struct {
		version string
		want    bool
	}{{"9.4.0", true}, {"8.4.6", false}, {"10.11.14-MariaDB", false}, {"5.5.5-11.4.8-MariaDB", false}} {
		db := &gorm.DB{Config: &gorm.Config{Dialector: mysql.New(mysql.Config{ServerVersion: tc.version})}}
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					Init(db)
					if UseRowAlias(db) != tc.want {
						t.Errorf("wrong SQL dialect for %s", tc.version)
						return
					}
				}
			}()
		}
	}
	wg.Wait()
}
