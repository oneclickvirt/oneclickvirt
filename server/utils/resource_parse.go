package utils

import (
	"strconv"
	"strings"
)

// ParseCPUCount accepts a plain core count or Linux/LXD CPU-set notation such
// as "0-3" and "0,2-3". Invalid or unbounded values return zero.
func ParseCPUCount(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if count, err := strconv.Atoi(value); err == nil {
		if count > 0 {
			return count
		}
		return 0
	}
	seen := make(map[int]struct{})
	for _, token := range strings.Split(value, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			return 0
		}
		if startText, endText, ranged := strings.Cut(token, "-"); ranged {
			start, startErr := strconv.Atoi(strings.TrimSpace(startText))
			end, endErr := strconv.Atoi(strings.TrimSpace(endText))
			if startErr != nil || endErr != nil || start < 0 || end < start || end-start > 4096 {
				return 0
			}
			for cpu := start; cpu <= end; cpu++ {
				seen[cpu] = struct{}{}
			}
			continue
		}
		cpu, err := strconv.Atoi(token)
		if err != nil || cpu < 0 {
			return 0
		}
		seen[cpu] = struct{}{}
	}
	return len(seen)
}
