package ipv6pool

import (
	"fmt"
	"math/big"
	"net"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
)

func (s *Service) GetPoolStats(providerID uint) (total, allocated, available int64) {
	stats, err := s.GetPoolStatsDetail(providerID)
	if err != nil {
		return 0, 0, 0
	}
	return stats.Entries, stats.Allocated, stats.Available
}

func (s *Service) GetPoolStatsDetail(providerID uint) (PoolStats, error) {
	var aggregate struct {
		Entries       int64
		Materialized  int64
		Ranges        int64
		OpenRanges    int64
		PendingRetire int64
		Allocated     int64
		Reusable      int64
	}
	if err := global.APP_DB.Model(&providerModel.ProviderIPv6Pool{}).
		Select(`COUNT(*) AS entries,
COALESCE(SUM(CASE WHEN is_range = 0 THEN 1 ELSE 0 END), 0) AS materialized,
COALESCE(SUM(CASE WHEN is_range = 1 THEN 1 ELSE 0 END), 0) AS ranges,
COALESCE(SUM(CASE WHEN is_range = 1 AND pending_retire = 0 AND range_next <> '' THEN 1 ELSE 0 END), 0) AS open_ranges,
COALESCE(SUM(CASE WHEN pending_retire = 1 THEN 1 ELSE 0 END), 0) AS pending_retire,
COALESCE(SUM(CASE WHEN is_range = 0 AND is_allocated = 1 THEN 1 ELSE 0 END), 0) AS allocated,
COALESCE(SUM(CASE WHEN is_range = 0 AND is_allocated = 0 AND pending_retire = 0 THEN 1 ELSE 0 END), 0) AS reusable`).
		Where("provider_id = ? AND deleted_at IS NULL", providerID).Scan(&aggregate).Error; err != nil {
		return PoolStats{}, err
	}

	var openRanges []providerModel.ProviderIPv6Pool
	if err := global.APP_DB.Select("address", "range_next").
		Where("provider_id = ? AND is_range = ? AND pending_retire = ? AND range_next <> '' AND deleted_at IS NULL", providerID, true, false).
		Find(&openRanges).Error; err != nil {
		return PoolStats{}, err
	}
	available := big.NewInt(aggregate.Reusable)
	for _, entry := range openRanges {
		remaining, err := remainingIPv6RangeCapacity(entry.Address, entry.RangeNext)
		if err != nil {
			return PoolStats{}, err
		}
		available.Add(available, remaining)
	}

	const maxInt64 = int64(^uint64(0) >> 1)
	numericAvailable := maxInt64
	saturated := !available.IsInt64()
	if !saturated {
		numericAvailable = available.Int64()
	}
	return PoolStats{
		Entries: aggregate.Entries, Materialized: aggregate.Materialized,
		Ranges: aggregate.Ranges, OpenRanges: aggregate.OpenRanges,
		PendingRetire: aggregate.PendingRetire, Allocated: aggregate.Allocated, Reusable: aggregate.Reusable,
		Available: numericAvailable, AvailableExact: available.String(),
		AvailableSaturated: saturated,
	}, nil
}

func remainingIPv6RangeCapacity(cidr, nextValue string) (*big.Int, error) {
	next := net.ParseIP(strings.TrimSpace(nextValue)).To16()
	_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil || network == nil || next == nil || !network.Contains(next) {
		return nil, fmt.Errorf("无效的IPv6范围游标: %s", cidr)
	}
	ones, bits := network.Mask.Size()
	if bits != 128 || ones < 0 || ones > 128 {
		return nil, fmt.Errorf("无效的IPv6范围前缀: %s", cidr)
	}
	base := new(big.Int).SetBytes(network.IP.To16())
	last := new(big.Int).Lsh(big.NewInt(1), uint(128-ones))
	last.Sub(last, big.NewInt(1)).Add(last, base)
	remaining := new(big.Int).Sub(last, new(big.Int).SetBytes(next))
	return remaining.Add(remaining, big.NewInt(1)), nil
}
