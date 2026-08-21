package console

import "sync"

// InstanceConsoleCacheInvalidator is registered by the API capability owner.
// Lifecycle services only know that an instance changed; they must not import
// the API package or reach into its probe cache directly.
type InstanceConsoleCacheInvalidator func(instanceID uint)

var instanceConsoleCacheInvalidator struct {
	sync.RWMutex
	callback InstanceConsoleCacheInvalidator
}

// RegisterInstanceConsoleCacheInvalidator installs the process-local cache
// invalidator used after an instance lifecycle transition. Registering a new
// callback replaces the previous one so test/bootstrap reinitialization stays
// deterministic.
func RegisterInstanceConsoleCacheInvalidator(callback InstanceConsoleCacheInvalidator) {
	instanceConsoleCacheInvalidator.Lock()
	instanceConsoleCacheInvalidator.callback = callback
	instanceConsoleCacheInvalidator.Unlock()
}

// InvalidateInstanceConsoleCaches is intentionally best-effort. The owning
// API cache has a short TTL, while this call removes it as soon as a committed
// lifecycle or port-mapping change makes the previous observation stale.
func InvalidateInstanceConsoleCaches(instanceID uint) {
	if instanceID == 0 {
		return
	}
	instanceConsoleCacheInvalidator.RLock()
	callback := instanceConsoleCacheInvalidator.callback
	instanceConsoleCacheInvalidator.RUnlock()
	if callback != nil {
		callback(instanceID)
	}
}
