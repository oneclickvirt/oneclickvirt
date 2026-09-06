package provider

import (
	"sort"
	"strings"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"

	"gorm.io/gorm"
)

// instanceMatchSet records one-to-one matches between discovered instances and
// database instances.  The indexes refer to the input slices.
type instanceMatchSet struct {
	RemoteToDB map[int]int
	DBToRemote map[int]int
}

type providerInstanceIDBackfill struct {
	InstanceID                 uint
	ProviderInstanceID         string
	PreviousProviderInstanceID string
}

const providerInstanceIDBackfillBatchSize = 50

// batchBackfillProviderInstanceIDs upgrades legacy identity rows with bounded
// CASE updates. The caller has already matched each row against one remote
// identity; keeping the update in a single statement per batch avoids an
// instance-sized write loop during recovery while retaining the existing
// provider_vm_id values for rows outside the batch.
func batchBackfillProviderInstanceIDs(db *gorm.DB, backfills []providerInstanceIDBackfill) error {
	if db == nil || len(backfills) == 0 {
		return nil
	}

	ordered := append([]providerInstanceIDBackfill(nil), backfills...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].InstanceID < ordered[j].InstanceID
	})
	for start := 0; start < len(ordered); start += providerInstanceIDBackfillBatchSize {
		end := start + providerInstanceIDBackfillBatchSize
		if end > len(ordered) {
			end = len(ordered)
		}
		batch := ordered[start:end]

		ids := make([]uint, 0, len(batch))
		var builder strings.Builder
		builder.WriteString("CASE id")
		args := make([]interface{}, 0, len(batch)*3)
		for _, backfill := range batch {
			ids = append(ids, backfill.InstanceID)
			// Keep the legacy guard inside CASE so a concurrent discovery cannot
			// overwrite a newer ProviderVMID while this bounded batch is writing.
			builder.WriteString(" WHEN ? THEN CASE WHEN provider_vm_id IS NULL OR provider_vm_id = ? OR provider_vm_id = '' THEN ? ELSE provider_vm_id END")
			args = append(args, backfill.InstanceID, backfill.PreviousProviderInstanceID, backfill.ProviderInstanceID)
		}
		builder.WriteString(" ELSE provider_vm_id END")

		if err := db.Model(&providerModel.Instance{}).
			Where("id IN ?", ids).
			Update("provider_vm_id", gorm.Expr(builder.String(), args...)).Error; err != nil {
			return err
		}
	}
	return nil
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
	remoteID := strings.TrimSpace(remote.ProviderInstanceID)
	dbID := strings.TrimSpace(db.ProviderVMID)
	if remoteID != "" && dbID != "" {
		if remoteID == dbID {
			return true
		}
		// Older generic create flows stored the remote name in provider_vm_id.
		// Permit a one-time name match so discovery can upgrade it to the
		// runtime-native ID. Arbitrary conflicting IDs remain authoritative.
		if !isProxmoxProviderType(providerType) && strings.TrimSpace(db.Name) != "" && dbID == strings.TrimSpace(db.Name) && strings.TrimSpace(remote.Name) == strings.TrimSpace(db.Name) {
			return true
		}
		return false
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

// providerInstanceIDBackfills upgrades legacy imported rows after they have
// been safely paired through the UUID/name compatibility path. Once backfilled,
// subsequent discovery remains stable even if the guest is renamed.
func providerInstanceIDBackfills(providerType string, remoteInstances []providerCore.DiscoveredInstance, dbInstances []providerModel.Instance, matches instanceMatchSet) []providerInstanceIDBackfill {
	backfills := make([]providerInstanceIDBackfill, 0)
	for remoteIndex, dbIndex := range matches.RemoteToDB {
		if remoteIndex < 0 || remoteIndex >= len(remoteInstances) || dbIndex < 0 || dbIndex >= len(dbInstances) {
			continue
		}
		dbInstance := dbInstances[dbIndex]
		remoteID := strings.TrimSpace(remoteInstances[remoteIndex].ProviderInstanceID)
		currentID := strings.TrimSpace(dbInstance.ProviderVMID)
		if dbInstance.ID == 0 || remoteID == "" || currentID == remoteID {
			continue
		}
		if currentID != "" && currentID != strings.TrimSpace(dbInstance.Name) {
			continue
		}
		backfills = append(backfills, providerInstanceIDBackfill{
			InstanceID:                 dbInstance.ID,
			ProviderInstanceID:         remoteID,
			PreviousProviderInstanceID: currentID,
		})
	}
	return backfills
}

func discoveredInstanceDeleteID(providerType string, remote providerCore.DiscoveredInstance) string {
	if remoteID := strings.TrimSpace(remote.ProviderInstanceID); remoteID != "" {
		return remoteID
	}
	if remoteUUID := strings.TrimSpace(remote.UUID); remoteUUID != "" {
		return remoteUUID
	}
	return strings.TrimSpace(remote.Name)
}
