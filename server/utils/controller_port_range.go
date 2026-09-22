package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	ControllerPortRangeStartEnv = "OCV_CONTROLLER_PORT_RANGE_START"
	ControllerPortRangeEndEnv   = "OCV_CONTROLLER_PORT_RANGE_END"

	DefaultControllerPortRangeStart = 10000
	DefaultControllerPortRangeEnd   = 65535
)

// ControllerPortRange describes the controller-owned TCP listener range.
// Configured is false for bare-metal/source deployments that retain the
// historical unrestricted 10000-65535 allocator. Container deployments set
// both environment variables so allocation and published Docker ports agree.
type ControllerPortRange struct {
	Start      int
	End        int
	Configured bool
}

func ResolveControllerPortRange() (ControllerPortRange, error) {
	startValue := strings.TrimSpace(os.Getenv(ControllerPortRangeStartEnv))
	endValue := strings.TrimSpace(os.Getenv(ControllerPortRangeEndEnv))
	if startValue == "" && endValue == "" {
		return ControllerPortRange{
			Start: DefaultControllerPortRangeStart,
			End:   DefaultControllerPortRangeEnd,
		}, nil
	}
	if startValue == "" || endValue == "" {
		return ControllerPortRange{}, fmt.Errorf("%s and %s must be configured together", ControllerPortRangeStartEnv, ControllerPortRangeEndEnv)
	}

	start, startErr := strconv.Atoi(startValue)
	end, endErr := strconv.Atoi(endValue)
	if startErr != nil || endErr != nil || start < 1 || end > 65535 || start > end {
		return ControllerPortRange{}, fmt.Errorf("invalid controller port range %q-%q: expected 1 <= start <= end <= 65535", startValue, endValue)
	}
	return ControllerPortRange{Start: start, End: end, Configured: true}, nil
}

func (r ControllerPortRange) Contains(start, count int) bool {
	if count <= 0 || start < r.Start || start > r.End {
		return false
	}
	return count-1 <= r.End-start
}
