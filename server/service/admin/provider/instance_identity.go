package provider

import (
	"strings"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
)

// instanceMatchSet records one-to-one matches between discovered instances and
// database instances.  The indexes refer to the input slices.
type instanceMatchSet struct {
	RemoteToDB map[int]int
	DBToRemote map[int]int
}

type providerInstanceIDBackfill struct {
	InstanceID         uint
	ProviderInstanceID string
}

func isProxmoxProviderType(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "proxmox", "proxmoxve", "pve":
		return true
	default:
		return false
	}
}

// discoveredInstanceMatchesDB centralizes remote identity handling.
//
// Proxmox VMID/CTID is the durable identity and remains stable when a guest is
// renamed.  UUID/name matching is retained only as a compatibility fallback
// for records created before provider_vm_id was populated.  When both sides
// have a VMID, a mismatch is authoritative and must not fall through to name
// matching (names can be reused).
func discoveredInstanceMatchesDB(providerType string, remote providerCore.DiscoveredInstance, db providerModel.Instance) bool {
	if isProxmoxProviderType(providerType) {
		remoteID := strings.TrimSpace(remote.ProviderInstanceID)
		dbID := strings.TrimSpace(db.ProviderVMID)
		if remoteID != "" && dbID != "" {
			return remoteID == dbID
		}
	}

	remoteUUID := strings.TrimSpace(remote.UUID)
	dbUUID := strings.TrimSpace(db.UUID)
	if remoteUUID != "" && dbUUID != "" && remoteUUID == dbUUID {
		return true
	}

	remoteName := strings.TrimSpace(remote.Name)
	dbName := strings.TrimSpace(db.Name)
	return remoteName != "" && dbName != "" && remoteName == dbName
}

func hasMatchingDBInstance(providerType string, remote providerCore.DiscoveredInstance, dbInstances []providerModel.Instance) bool {
	for i := range dbInstances {
		if discoveredInstanceMatchesDB(providerType, remote, dbInstances[i]) {
			return true
		}
	}
	return false
}

// matchDiscoveredAndDBInstances performs one-to-one matching so duplicate or
// malformed discovery rows cannot make one database instance satisfy multiple
// remote instances.
func matchDiscoveredAndDBInstances(providerType string, remoteInstances []providerCore.DiscoveredInstance, dbInstances []providerModel.Instance) instanceMatchSet {
	matches := instanceMatchSet{
		RemoteToDB: make(map[int]int),
		DBToRemote: make(map[int]int),
	}

	// Match stable Proxmox identities first. This ensures a legacy name match
	// cannot consume a row that has an exact VMID counterpart later in the list.
	if isProxmoxProviderType(providerType) {
		for remoteIndex := range remoteInstances {
			remoteID := strings.TrimSpace(remoteInstances[remoteIndex].ProviderInstanceID)
			if remoteID == "" {
				continue
			}
			for dbIndex := range dbInstances {
				if _, used := matches.DBToRemote[dbIndex]; used {
					continue
				}
				dbID := strings.TrimSpace(dbInstances[dbIndex].ProviderVMID)
				if dbID != "" && remoteID == dbID {
					matches.RemoteToDB[remoteIndex] = dbIndex
					matches.DBToRemote[dbIndex] = remoteIndex
					break
				}
			}
		}
	}

	// Compatibility pass for non-Proxmox providers and legacy Proxmox rows
	// without provider_vm_id.
	for remoteIndex := range remoteInstances {
		if _, matched := matches.RemoteToDB[remoteIndex]; matched {
			continue
		}
		for dbIndex := range dbInstances {
			if _, used := matches.DBToRemote[dbIndex]; used {
				continue
			}
			if discoveredInstanceMatchesDB(providerType, remoteInstances[remoteIndex], dbInstances[dbIndex]) {
				matches.RemoteToDB[remoteIndex] = dbIndex
				matches.DBToRemote[dbIndex] = remoteIndex
				break
			}
		}
	}

	return matches
}

// providerInstanceIDBackfills upgrades legacy Proxmox rows after they have
// been safely paired through the UUID/name compatibility path. Once backfilled,
// subsequent discovery remains stable even if the guest is renamed.
func providerInstanceIDBackfills(providerType string, remoteInstances []providerCore.DiscoveredInstance, dbInstances []providerModel.Instance, matches instanceMatchSet) []providerInstanceIDBackfill {
	if !isProxmoxProviderType(providerType) {
		return nil
	}

	backfills := make([]providerInstanceIDBackfill, 0)
	for remoteIndex, dbIndex := range matches.RemoteToDB {
		if remoteIndex < 0 || remoteIndex >= len(remoteInstances) || dbIndex < 0 || dbIndex >= len(dbInstances) {
			continue
		}
		dbInstance := dbInstances[dbIndex]
		remoteID := strings.TrimSpace(remoteInstances[remoteIndex].ProviderInstanceID)
		if dbInstance.ID == 0 || strings.TrimSpace(dbInstance.ProviderVMID) != "" || remoteID == "" {
			continue
		}
		backfills = append(backfills, providerInstanceIDBackfill{
			InstanceID:         dbInstance.ID,
			ProviderInstanceID: remoteID,
		})
	}
	return backfills
}

func discoveredInstanceDeleteID(providerType string, remote providerCore.DiscoveredInstance) string {
	if isProxmoxProviderType(providerType) {
		if remoteID := strings.TrimSpace(remote.ProviderInstanceID); remoteID != "" {
			return remoteID
		}
	}
	if remoteUUID := strings.TrimSpace(remote.UUID); remoteUUID != "" {
		return remoteUUID
	}
	return strings.TrimSpace(remote.Name)
}
