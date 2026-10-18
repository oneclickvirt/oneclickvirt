package task

import (
	"context"
	"errors"
	providerModel "oneclickvirt/model/provider"
	"testing"
)

type resetRuntimePublicIPv6Fixture struct {
	address string
	err     error
	calls   int
}

func (f *resetRuntimePublicIPv6Fixture) GetInstance(ctx context.Context, name string) (*providerModel.ProviderInstance, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &providerModel.ProviderInstance{IPv6Address: f.address}, nil
}
func TestResetRuntimePublicIPv6FromReplacementGuest(t *testing.T) {
	for _, tc := range []struct {
		name, address, want string
		err                 error
	}{
		{name: "routed GUA", address: "2607:9d00:2000:a6::1001", want: "2607:9d00:2000:a6::1001"},
		{name: "ULA is private", address: "fd42::5"},
		{name: "link local is private", address: "fe80::5"},
		{name: "invalid output", address: "warning: failed"},
		{name: "lookup failure", err: errors.New("lookup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &resetRuntimePublicIPv6Fixture{address: tc.address, err: tc.err}
			if got := getResetInstancePublicIPv6(context.Background(), f, "replacement"); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &resetRuntimePublicIPv6Fixture{address: "2607:9d00:2000:a6::1001"}
	if got := getResetInstancePublicIPv6(ctx, f, "replacement"); got != "" || f.calls != 0 {
		t.Fatal("cancelled lookup queried provider")
	}
}
