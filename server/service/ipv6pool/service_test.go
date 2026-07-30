package ipv6pool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var ipv6PoolTestDBSequence atomic.Uint64

func setupIPv6PoolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:ipv6pool_%d?mode=memory&cache=shared", ipv6PoolTestDBSequence.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec(`CREATE TABLE providers (
		id integer PRIMARY KEY,
		ipv6_address_file_path text,
		ipv6_address_file_synced_at datetime,
		ipv6_address_file_sync_error text,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create providers table: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.ProviderIPv6Pool{}); err != nil {
		t.Fatalf("migrate IPv6 pool: %v", err)
	}
	if err := db.Exec("INSERT INTO providers (id, ipv6_address_file_path) VALUES (?, ?)", 1, "/etc/oneclickvirt/ipv6-pool.txt").Error; err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	previous := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() {
		global.APP_DB = previous
		_ = sqlDB.Close()
	})
	return db
}

func TestParseNodeFileRejectsDiagnosticOutputContainingIPv6(t *testing.T) {
	input := "2001:db8::10/128\n" +
		"inet6 2001:db8::20/64 scope global dynamic mngtmpaddr\n" +
		"valid_lft 86397sec preferred_lft 14397sec\n"

	parsed, invalid, err := parseIPv6PoolTextWithOptions(7, input, SourceNodeFile, true)
	if err == nil {
		t.Fatal("expected polluted node-file output to be rejected")
	}
	if parsed != nil {
		t.Fatalf("parsed = %#v, want nil on fail-closed node-file parsing", parsed)
	}
	if len(invalid) != 2 {
		t.Fatalf("invalid = %#v, want both polluted lines", invalid)
	}
}

func TestParseNodeFileAcceptsOneEntryPerLineAndComments(t *testing.T) {
	input := "# allocated IPv6 values\n" +
		"2001:db8::10 # discrete address\n" +
		"2001:db8:1::/127\n\n"

	parsed, invalid, err := parseIPv6PoolTextWithOptions(7, input, SourceNodeFile, true)
	if err != nil {
		t.Fatalf("parseIPv6PoolTextWithOptions() error = %v", err)
	}
	if len(invalid) != 0 {
		t.Fatalf("invalid = %#v, want none", invalid)
	}
	if len(parsed) != 2 {
		t.Fatalf("parsed = %#v, want two entries", parsed)
	}
	if parsed[0].Address != "2001:db8::10" || parsed[0].IsRange {
		t.Fatalf("unexpected discrete entry: %#v", parsed[0])
	}
	if parsed[1].Address != "2001:db8:1::/127" || !parsed[1].IsRange {
		t.Fatalf("unexpected range entry: %#v", parsed[1])
	}
}

func TestParseNodeFileAllowsEmptyContent(t *testing.T) {
	parsed, invalid, err := parseIPv6PoolTextWithOptions(7, "\n# no addresses\n", SourceNodeFile, true)
	if err != nil {
		t.Fatalf("parseIPv6PoolTextWithOptions() error = %v", err)
	}
	if len(parsed) != 0 || len(invalid) != 0 {
		t.Fatalf("parsed = %#v, invalid = %#v, want empty results", parsed, invalid)
	}
}

func TestParseNodeFileRejectsDecoratedAddress(t *testing.T) {
	parsed, invalid, err := parseIPv6PoolTextWithOptions(7, "[2001:db8::10]\n", SourceNodeFile, true)
	if err == nil {
		t.Fatal("expected decorated node-file address to be rejected")
	}
	if parsed != nil || len(invalid) != 1 {
		t.Fatalf("parsed = %#v, invalid = %#v", parsed, invalid)
	}

	parsed, invalid, err = parseIPv6PoolText(7, "[2001:db8::10]", SourceManual)
	if err != nil || len(parsed) != 1 || len(invalid) != 0 {
		t.Fatalf("manual decorated address parsed = %#v, invalid = %#v, err = %v", parsed, invalid, err)
	}
}

func TestParseManualPoolKeepsMultiValueAndPartialSuccess(t *testing.T) {
	input := "2001:db8::10, 2001:db8::11;not-an-address 2001:db8:1::/126"

	parsed, invalid, err := parseIPv6PoolText(7, input, SourceManual)
	if err != nil {
		t.Fatalf("parseIPv6PoolText() error = %v", err)
	}
	if len(parsed) != 3 {
		t.Fatalf("parsed = %#v, want three valid entries", parsed)
	}
	if len(invalid) != 1 {
		t.Fatalf("invalid = %#v, want one invalid token", invalid)
	}
}

func TestParseIPv6PoolPrefixBoundaries(t *testing.T) {
	tests := []struct {
		input         string
		wantAddress   string
		wantPrefix    int
		wantIsRange   bool
		wantRangeNext string
	}{
		{input: "2001:db8:1:2:3::/80", wantAddress: "2001:db8:1:2:3::/80", wantPrefix: 80, wantIsRange: true, wantRangeNext: "2001:db8:1:2:3::"},
		{input: "2001:db8:1:2:3:4::/96", wantAddress: "2001:db8:1:2:3:4::/96", wantPrefix: 96, wantIsRange: true, wantRangeNext: "2001:db8:1:2:3:4::"},
		{input: "2001:db8:1:2:3:4:5::/112", wantAddress: "2001:db8:1:2:3:4:5:0/112", wantPrefix: 112, wantIsRange: true, wantRangeNext: "2001:db8:1:2:3:4:5:0"},
		{input: "2001:db8::/126", wantAddress: "2001:db8::/126", wantPrefix: 126, wantIsRange: true, wantRangeNext: "2001:db8::"},
		{input: "2001:db8::/127", wantAddress: "2001:db8::/127", wantPrefix: 127, wantIsRange: true, wantRangeNext: "2001:db8::"},
		{input: "2001:db8::7/128", wantAddress: "2001:db8::7", wantPrefix: 128, wantIsRange: false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			parsed, invalid, err := parseIPv6PoolText(9, test.input, SourceManual)
			if err != nil {
				t.Fatalf("parseIPv6PoolText() error = %v", err)
			}
			if len(invalid) != 0 || len(parsed) != 1 {
				t.Fatalf("parsed = %#v, invalid = %#v", parsed, invalid)
			}
			entry := parsed[0]
			if entry.Address != test.wantAddress || entry.PrefixLength != test.wantPrefix || entry.IsRange != test.wantIsRange || entry.RangeNext != test.wantRangeNext {
				t.Fatalf("entry = %#v", entry)
			}
		})
	}

	if _, _, err := parseIPv6PoolText(9, "2001:db8::/129", SourceManual); err == nil {
		t.Fatal("expected invalid prefix to fail")
	}
}

func TestIPv6RangeCandidateWindowIncludesEverySmallPrefixAddress(t *testing.T) {
	tests := []struct {
		cidr string
		want []string
	}{
		{cidr: "2001:db8::/126", want: []string{"2001:db8::", "2001:db8::1", "2001:db8::2", "2001:db8::3"}},
		{cidr: "2001:db8:1::/127", want: []string{"2001:db8:1::", "2001:db8:1::1"}},
	}

	for _, test := range tests {
		t.Run(test.cidr, func(t *testing.T) {
			source := providerModel.ProviderIPv6Pool{Address: test.cidr, RangeNext: test.want[0], IsRange: true}
			got, next, err := ipv6RangeCandidateWindow(source, 1024)
			if err != nil {
				t.Fatalf("ipv6RangeCandidateWindow() error = %v", err)
			}
			if next != "" {
				t.Fatalf("next = %q, want exhausted cursor", next)
			}
			if len(got) != len(test.want) {
				t.Fatalf("got = %#v, want %#v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("got[%d] = %q, want %q", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestIPv6RangeCandidateWindowReturnsNextCursor(t *testing.T) {
	source := providerModel.ProviderIPv6Pool{Address: "2001:db8::/120", RangeNext: "2001:db8::10", IsRange: true}
	got, next, err := ipv6RangeCandidateWindow(source, 3)
	if err != nil {
		t.Fatalf("ipv6RangeCandidateWindow() error = %v", err)
	}
	want := []string{"2001:db8::10", "2001:db8::11", "2001:db8::12"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got = %#v, want %#v", got, want)
		}
	}
	if next != "2001:db8::13" {
		t.Fatalf("next = %q, want 2001:db8::13", next)
	}
}

func TestAllocateIPv6AddressSkipsMoreThanLegacyWindowLimitWithoutRetry(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	_, network, err := net.ParseCIDR("2001:db8:3::/112")
	if err != nil {
		t.Fatal(err)
	}
	parent := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: network.String(), PrefixLength: 112,
		IsRange: true, RangeNext: network.IP.String(), Source: SourceManual,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create range: %v", err)
	}

	occupied := make([]providerModel.ProviderIPv6Pool, 0, 8193)
	candidate := network.IP.To16()
	for index := 0; index < 8193; index++ {
		instanceID := uint(index + 1000)
		occupied = append(occupied, providerModel.ProviderIPv6Pool{
			ProviderID: 1, Address: candidate.String(), PrefixLength: 128,
			IsAllocated: true, InstanceID: &instanceID, Source: SourceManual,
		})
		candidate, _ = incrementIPv6(candidate)
	}
	if err := db.CreateInBatches(&occupied, 50).Error; err != nil {
		t.Fatalf("create occupied addresses: %v", err)
	}

	got, err := NewService().AllocateIPv6Address(1, 99)
	if err != nil {
		t.Fatalf("AllocateIPv6Address() error = %v", err)
	}
	if got != "2001:db8:3::2001" {
		t.Fatalf("allocated = %q, want 2001:db8:3::2001", got)
	}
}

func TestClearUnallocatedPreservesOnlyAllocatedBindingsAndTheirRanges(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	service := NewService()
	var deleteStatements []string
	if err := db.Callback().Delete().After("gorm:delete").Register("test:capture_ipv6_pool_deletes", func(tx *gorm.DB) {
		deleteStatements = append(deleteStatements, tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
	}); err != nil {
		t.Fatalf("register delete SQL capture: %v", err)
	}

	allocatedInstanceID := uint(901)
	allocatedDiscrete := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::901", PrefixLength: 128,
		IsAllocated: true, InstanceID: &allocatedInstanceID, Source: SourceManual,
	}
	unallocatedDiscrete := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::902", PrefixLength: 128, Source: SourceManual,
	}
	protectedRange := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1::/126", PrefixLength: 126,
		IsRange: true, RangeNext: "2001:db8:1::3", Source: SourceManual,
	}
	emptyRange := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:2::/126", PrefixLength: 126,
		IsRange: true, RangeNext: "2001:db8:2::", Source: SourceManual,
	}
	releasedOnlyRange := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:3::/126", PrefixLength: 126,
		IsRange: true, RangeNext: "2001:db8:3::2", Source: SourceManual,
	}
	otherProviderEntry := providerModel.ProviderIPv6Pool{
		ProviderID: 2, Address: "2001:db8:ffff::1", PrefixLength: 128, Source: SourceManual,
	}
	for _, entry := range []*providerModel.ProviderIPv6Pool{
		&allocatedDiscrete, &unallocatedDiscrete, &protectedRange, &emptyRange, &releasedOnlyRange, &otherProviderEntry,
	} {
		if err := db.Create(entry).Error; err != nil {
			t.Fatalf("create IPv6 pool entry: %v", err)
		}
	}

	allocatedRangeInstanceID := uint(903)
	allocatedChild := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1::1", PrefixLength: 128,
		ParentID: &protectedRange.ID, IsAllocated: true, InstanceID: &allocatedRangeInstanceID, Source: SourceRangeChild,
	}
	releasedProtectedChild := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1::2", PrefixLength: 128,
		ParentID: &protectedRange.ID, Source: SourceRangeChild,
	}
	releasedOnlyChild := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:3::1", PrefixLength: 128,
		ParentID: &releasedOnlyRange.ID, Source: SourceRangeChild,
	}
	for _, entry := range []*providerModel.ProviderIPv6Pool{&allocatedChild, &releasedProtectedChild, &releasedOnlyChild} {
		if err := db.Create(entry).Error; err != nil {
			t.Fatalf("create IPv6 range child: %v", err)
		}
	}

	deleted, err := service.ClearUnallocated(1)
	if err != nil {
		t.Fatalf("ClearUnallocated() error = %v", err)
	}
	if deleted != 5 {
		t.Fatalf("ClearUnallocated() deleted = %d, want 5", deleted)
	}
	if len(deleteStatements) != 2 {
		t.Fatalf("ClearUnallocated() issued %d delete statements, want 2: %#v", len(deleteStatements), deleteStatements)
	}
	for _, statement := range deleteStatements {
		if strings.Contains(strings.ToUpper(statement), "SELECT") {
			t.Fatalf("ClearUnallocated() generated a delete subquery incompatible with MySQL 1093: %s", statement)
		}
	}

	for _, id := range []uint{allocatedDiscrete.ID, protectedRange.ID, allocatedChild.ID, otherProviderEntry.ID} {
		var count int64
		if err := db.Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", id).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("preserved IPv6 pool entry %d count = %d, want 1", id, count)
		}
	}
	for _, id := range []uint{unallocatedDiscrete.ID, emptyRange.ID, releasedOnlyRange.ID, releasedProtectedChild.ID, releasedOnlyChild.ID} {
		var count int64
		if err := db.Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", id).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("cleared IPv6 pool entry %d count = %d, want 0", id, count)
		}
	}
}

func TestValidateNodeFilePathRejectsNonCanonicalOrControlContent(t *testing.T) {
	for _, value := range []string{"relative/pool.txt", "/etc/../tmp/pool.txt", "/etc/oneclickvirt/pool\n.txt"} {
		if _, err := ValidateNodeFilePath(value); err == nil {
			t.Fatalf("ValidateNodeFilePath(%q) unexpectedly succeeded", value)
		}
	}
	if got, err := ValidateNodeFilePath(" /etc/oneclickvirt/ipv6-pool.txt "); err != nil || got != "/etc/oneclickvirt/ipv6-pool.txt" {
		t.Fatalf("ValidateNodeFilePath() = %q, %v", got, err)
	}
}

func TestRemainingIPv6RangeCapacityUsesArbitraryPrecision(t *testing.T) {
	tests := []struct {
		cidr string
		next string
		want string
	}{
		{cidr: "2001:db8::/127", next: "2001:db8::", want: "2"},
		{cidr: "2001:db8::/64", next: "2001:db8::", want: "18446744073709551616"},
		{cidr: "2001:db8:1::/80", next: "2001:db8:1::", want: "281474976710656"},
	}

	for _, test := range tests {
		t.Run(test.cidr, func(t *testing.T) {
			got, err := remainingIPv6RangeCapacity(test.cidr, test.next)
			if err != nil {
				t.Fatalf("remainingIPv6RangeCapacity() error = %v", err)
			}
			if got.String() != test.want {
				t.Fatalf("capacity = %s, want %s", got, test.want)
			}
		})
	}
}

func TestSupportsStaticIPv6ProviderMatrix(t *testing.T) {
	supported := []string{"lxd", "incus", "proxmox", "proxmoxve", "docker", "podman", "containerd", "orbstack", " LXD "}
	for _, providerType := range supported {
		if !SupportsStaticIPv6(providerType) {
			t.Fatalf("SupportsStaticIPv6(%q) = false, want true", providerType)
		}
	}
	unsupported := []string{"qemu", "kubevirt", "libvirt", "openstack", "unknown", ""}
	for _, providerType := range unsupported {
		if SupportsStaticIPv6(providerType) {
			t.Fatalf("SupportsStaticIPv6(%q) = true, want false", providerType)
		}
	}
}

func TestNodeFileRetiresAllocatedDiscreteAddressUntilRelease(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	instanceID := uint(41)
	entry := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::41", PrefixLength: 128,
		IsAllocated: true, InstanceID: &instanceID, Source: SourceNodeFile,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create allocated address: %v", err)
	}

	result := SyncResult{SyncedAt: time.Now()}
	service := NewService()
	if err := service.reconcileNodeFile(1, nil, &result); err != nil {
		t.Fatalf("reconcileNodeFile() error = %v", err)
	}
	var retired providerModel.ProviderIPv6Pool
	if err := db.First(&retired, entry.ID).Error; err != nil {
		t.Fatalf("load retired address: %v", err)
	}
	if !retired.PendingRetire || !retired.IsAllocated || retired.InstanceID == nil || *retired.InstanceID != instanceID {
		t.Fatalf("retired binding was not preserved: %#v", retired)
	}
	if got, err := service.GetAllocatedAddress(instanceID); err != nil || got != entry.Address {
		t.Fatalf("GetAllocatedAddress() = %q, %v", got, err)
	}
	if _, err := service.AllocateIPv6Address(1, 42); err == nil || !strings.Contains(err.Error(), "已耗尽") {
		t.Fatalf("pending-retire address should not allocate, err = %v", err)
	}

	if err := service.ReleaseIPv6(instanceID); err != nil {
		t.Fatalf("ReleaseIPv6() error = %v", err)
	}
	var count int64
	if err := db.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", entry.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("retired discrete row count = %d, want 0 after release", count)
	}
	if _, err := service.AllocateIPv6Address(1, 43); err == nil {
		t.Fatal("released retired address became allocatable again")
	}
}

func TestNodeFileRetiresRangeWithoutDroppingChildBinding(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	parent := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1::/126", PrefixLength: 126,
		IsRange: true, RangeNext: "2001:db8:1::3", Source: SourceNodeFile,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create range: %v", err)
	}
	instanceID := uint(51)
	allocatedChild := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1::1", PrefixLength: 128,
		ParentID: &parent.ID, IsAllocated: true, InstanceID: &instanceID, Source: SourceRangeChild,
	}
	releasedChild := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1::2", PrefixLength: 128,
		ParentID: &parent.ID, Source: SourceRangeChild,
	}
	if err := db.Create(&allocatedChild).Error; err != nil {
		t.Fatalf("create allocated child: %v", err)
	}
	if err := db.Create(&releasedChild).Error; err != nil {
		t.Fatalf("create released child: %v", err)
	}

	result := SyncResult{SyncedAt: time.Now()}
	service := NewService()
	if err := service.reconcileNodeFile(1, nil, &result); err != nil {
		t.Fatalf("reconcileNodeFile() error = %v", err)
	}
	var retiredParent providerModel.ProviderIPv6Pool
	if err := db.First(&retiredParent, parent.ID).Error; err != nil {
		t.Fatalf("load retired range: %v", err)
	}
	if !retiredParent.PendingRetire {
		t.Fatalf("range pending_retire = false: %#v", retiredParent)
	}
	var boundChild providerModel.ProviderIPv6Pool
	if err := db.First(&boundChild, allocatedChild.ID).Error; err != nil {
		t.Fatalf("allocated child was removed: %v", err)
	}
	if !boundChild.IsAllocated || boundChild.InstanceID == nil || *boundChild.InstanceID != instanceID {
		t.Fatalf("allocated child binding changed: %#v", boundChild)
	}
	var releasedCount int64
	if err := db.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", releasedChild.ID).Count(&releasedCount).Error; err != nil {
		t.Fatal(err)
	}
	if releasedCount != 0 {
		t.Fatalf("unallocated child count = %d, want 0 on range retirement", releasedCount)
	}
	if _, err := service.AllocateIPv6Address(1, 52); err == nil {
		t.Fatal("pending-retire range remained allocatable")
	}

	if err := service.ReleaseIPv6(instanceID); err != nil {
		t.Fatalf("ReleaseIPv6() error = %v", err)
	}
	var parentCount, childCount int64
	if err := db.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", parent.ID).Count(&parentCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("parent_id = ?", parent.ID).Count(&childCount).Error; err != nil {
		t.Fatal(err)
	}
	if parentCount != 0 || childCount != 0 {
		t.Fatalf("retired range cleanup left parent=%d children=%d", parentCount, childCount)
	}
	if _, err := service.AllocateIPv6Address(1, 53); err == nil {
		t.Fatal("released retired range became allocatable again")
	}
}

func TestNodeFileReaddCancelsPendingRetireForDiscreteBinding(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	instanceID := uint(61)
	entry := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::61", PrefixLength: 128,
		IsAllocated: true, InstanceID: &instanceID, Source: SourceNodeFile,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create allocated address: %v", err)
	}
	service := NewService()
	if err := service.reconcileNodeFile(1, nil, &SyncResult{SyncedAt: time.Now()}); err != nil {
		t.Fatalf("retire address: %v", err)
	}
	if err := service.reconcileNodeFile(1, []providerModel.ProviderIPv6Pool{{
		ProviderID: 1, Address: entry.Address, PrefixLength: 128, Source: SourceNodeFile,
	}}, &SyncResult{SyncedAt: time.Now()}); err != nil {
		t.Fatalf("re-add retired address: %v", err)
	}
	var restored providerModel.ProviderIPv6Pool
	if err := db.First(&restored, entry.ID).Error; err != nil {
		t.Fatalf("load restored address: %v", err)
	}
	if restored.PendingRetire || !restored.IsAllocated || restored.InstanceID == nil || *restored.InstanceID != instanceID {
		t.Fatalf("restored binding = %#v", restored)
	}
	if err := service.ReleaseIPv6(instanceID); err != nil {
		t.Fatalf("release restored address: %v", err)
	}
	if got, err := service.AllocateIPv6Address(1, 62); err != nil || got != entry.Address {
		t.Fatalf("re-added address allocation = %q, %v; want %q", got, err, entry.Address)
	}
}

func TestNodeFileReaddCancelsPendingRetireForRangeBinding(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	parent := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:2::/126", PrefixLength: 126,
		IsRange: true, RangeNext: "2001:db8:2::2", Source: SourceNodeFile,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create range: %v", err)
	}
	instanceID := uint(71)
	child := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:2::1", PrefixLength: 128,
		ParentID: &parent.ID, IsAllocated: true, InstanceID: &instanceID, Source: SourceRangeChild,
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create allocated child: %v", err)
	}
	service := NewService()
	if err := service.reconcileNodeFile(1, nil, &SyncResult{SyncedAt: time.Now()}); err != nil {
		t.Fatalf("retire range: %v", err)
	}
	if err := service.reconcileNodeFile(1, []providerModel.ProviderIPv6Pool{{
		ProviderID: 1, Address: parent.Address, PrefixLength: parent.PrefixLength,
		IsRange: true, RangeNext: parent.RangeNext, Source: SourceNodeFile,
	}}, &SyncResult{SyncedAt: time.Now()}); err != nil {
		t.Fatalf("re-add retired range: %v", err)
	}
	var restoredParent providerModel.ProviderIPv6Pool
	if err := db.First(&restoredParent, parent.ID).Error; err != nil {
		t.Fatalf("load restored range: %v", err)
	}
	if restoredParent.PendingRetire {
		t.Fatalf("restored range remains pending_retire: %#v", restoredParent)
	}
	if err := service.ReleaseIPv6(instanceID); err != nil {
		t.Fatalf("release restored range child: %v", err)
	}
	if got, err := service.AllocateIPv6Address(1, 72); err != nil || got != child.Address {
		t.Fatalf("re-added range allocation = %q, %v; want released child %q", got, err, child.Address)
	}
}

func TestSyncProviderFileReadsNodeOnceBeforeReconcile(t *testing.T) {
	setupIPv6PoolTestDB(t)
	readCount := 0
	service := &Service{readNodeFile: func(_ context.Context, providerID uint, path string) (string, error) {
		readCount++
		if providerID != 1 || path != "/etc/oneclickvirt/ipv6-pool.txt" {
			t.Fatalf("reader arguments = provider %d, path %q", providerID, path)
		}
		return "2001:db8::90\n2001:db8:2::/127\n", nil
	}}
	result, err := service.SyncProviderFile(context.Background(), 1)
	if err != nil {
		t.Fatalf("SyncProviderFile() error = %v", err)
	}
	if readCount != 1 || result.RemoteReadCount != 1 || result.ParsedCount != 2 {
		t.Fatalf("readCount=%d result=%#v", readCount, result)
	}
}

func TestPoolStatsReportsLargeRangeCapacityWithoutOverflow(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	entry := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::/64", PrefixLength: 64,
		IsRange: true, RangeNext: "2001:db8::", Source: SourceManual,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create range: %v", err)
	}
	stats, err := NewService().GetPoolStatsDetail(1)
	if err != nil {
		t.Fatalf("GetPoolStatsDetail() error = %v", err)
	}
	if stats.AvailableExact != "18446744073709551616" || !stats.AvailableSaturated || stats.Available != int64(^uint64(0)>>1) {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestTransferIPv6BindingWithDBPreservesAndIdempotentlyMovesAllocation(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	oldInstanceID := uint(801)
	newInstanceID := uint(802)
	entry := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::801", PrefixLength: 128,
		IsAllocated: true, InstanceID: &oldInstanceID, Source: SourceNodeFile,
		PendingRetire: true,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create allocated address: %v", err)
	}

	service := NewService()
	var address string
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		address, err = service.TransferIPv6BindingWithDB(tx, 1, oldInstanceID, newInstanceID)
		return err
	}); err != nil {
		t.Fatalf("TransferIPv6BindingWithDB() error = %v", err)
	}
	if address != entry.Address {
		t.Fatalf("transferred address = %q, want %q", address, entry.Address)
	}

	var moved providerModel.ProviderIPv6Pool
	if err := db.First(&moved, entry.ID).Error; err != nil {
		t.Fatalf("load transferred address: %v", err)
	}
	if !moved.IsAllocated || !moved.PendingRetire || moved.InstanceID == nil || *moved.InstanceID != newInstanceID {
		t.Fatalf("transferred binding = %#v", moved)
	}
	if got, err := service.GetAllocatedAddress(oldInstanceID); err != nil || got != "" {
		t.Fatalf("old instance allocation = %q, %v", got, err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		got, err := service.TransferIPv6BindingWithDB(tx, 1, oldInstanceID, newInstanceID)
		if err != nil {
			return err
		}
		if got != entry.Address {
			return fmt.Errorf("idempotent address = %q", got)
		}
		return nil
	}); err != nil {
		t.Fatalf("idempotent transfer error = %v", err)
	}
}

func TestTransferIPv6BindingWithDBRollsBackWithReplacementRecord(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	oldInstanceID := uint(811)
	newInstanceID := uint(812)
	entry := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8::811", PrefixLength: 128,
		IsAllocated: true, InstanceID: &oldInstanceID, Source: SourceManual,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create allocated address: %v", err)
	}

	rollback := errors.New("rollback replacement transaction")
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := NewService().TransferIPv6BindingWithDB(tx, 1, oldInstanceID, newInstanceID); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v, want rollback", err)
	}
	if got, err := NewService().GetAllocatedAddress(oldInstanceID); err != nil || got != entry.Address {
		t.Fatalf("old allocation after rollback = %q, %v", got, err)
	}
	if got, err := NewService().GetAllocatedAddress(newInstanceID); err != nil || got != "" {
		t.Fatalf("new allocation after rollback = %q, %v", got, err)
	}
}

func TestTransferIPv6BindingWithDBRejectsTwoLiveBindings(t *testing.T) {
	db := setupIPv6PoolTestDB(t)
	oldInstanceID := uint(821)
	newInstanceID := uint(822)
	entries := []providerModel.ProviderIPv6Pool{
		{ProviderID: 1, Address: "2001:db8::821", PrefixLength: 128, IsAllocated: true, InstanceID: &oldInstanceID, Source: SourceManual},
		{ProviderID: 1, Address: "2001:db8::822", PrefixLength: 128, IsAllocated: true, InstanceID: &newInstanceID, Source: SourceManual},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatalf("create allocated addresses: %v", err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := NewService().TransferIPv6BindingWithDB(tx, 1, oldInstanceID, newInstanceID)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "同时存在") {
		t.Fatalf("transfer error = %v, want ambiguous binding rejection", err)
	}
	if got, _ := NewService().GetAllocatedAddress(oldInstanceID); got != entries[0].Address {
		t.Fatalf("old binding changed to %q", got)
	}
	if got, _ := NewService().GetAllocatedAddress(newInstanceID); got != entries[1].Address {
		t.Fatalf("new binding changed to %q", got)
	}
}
