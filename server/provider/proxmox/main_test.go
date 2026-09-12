package proxmox

import (
	"go.uber.org/zap"
	"oneclickvirt/global"
	"os"
	"testing"
)

// Production initializes logging before providers. Tests must not depend on
// another test leaving its logger behind to satisfy that invariant.
func TestMain(m *testing.M) {
	global.APP_LOG = zap.NewNop()
	os.Exit(m.Run())
}
