package trafficmanual

import (
	"reflect"
	"testing"
)

func TestUniqueSortedIDsDropsZeroDuplicatesAndOrders(t *testing.T) {
	if got, want := uniqueSortedUserIDs([]uint{9, 0, 3, 9, 5, 3}), []uint{3, 5, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueSortedUserIDs() = %v, want %v", got, want)
	}
}

func TestUniqueSortedProviderIDsKeepsEmptyScopeConcrete(t *testing.T) {
	if got := uniqueSortedProviderIDs([]uint{0, 0}); got == nil || len(got) != 0 {
		t.Fatalf("uniqueSortedProviderIDs() = %#v, want non-nil empty slice", got)
	}
}
