package traffic

import (
	"reflect"
	"testing"
)

func TestNormalizeTrafficUserIDsDropsZeroDuplicatesAndOrders(t *testing.T) {
	got := normalizeTrafficUserIDs([]uint{9, 0, 3, 9, 5, 3})
	if want := []uint{3, 5, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeTrafficUserIDs() = %v, want %v", got, want)
	}
}
