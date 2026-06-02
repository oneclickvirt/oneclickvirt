package instance

import "sync"

type adminInstanceLockEntry struct {
	mu    sync.Mutex
	count int
}

var (
	adminInstanceLocksMu sync.Mutex
	adminInstanceLocks   = make(map[uint]*adminInstanceLockEntry)
)

func getAdminInstanceActionLock(instanceID uint) *adminInstanceLockEntry {
	adminInstanceLocksMu.Lock()
	lk := adminInstanceLocks[instanceID]
	if lk == nil {
		lk = &adminInstanceLockEntry{}
		adminInstanceLocks[instanceID] = lk
	}
	lk.count++
	adminInstanceLocksMu.Unlock()
	return lk
}

func releaseAdminInstanceActionLock(instanceID uint) {
	adminInstanceLocksMu.Lock()
	lk := adminInstanceLocks[instanceID]
	if lk != nil {
		lk.count--
		if lk.count == 0 {
			delete(adminInstanceLocks, instanceID)
		}
	}
	adminInstanceLocksMu.Unlock()
}
