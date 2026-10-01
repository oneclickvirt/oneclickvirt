package domain

import (
	"context"
	"errors"
	"oneclickvirt/global"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"
	"testing"
)

func TestDomainTargetIsAnExactInstanceAddress(t *testing.T) {
	provider := providerModel.Provider{Endpoint: "https://192.0.2.1:8443", AgentRemoteIP: "2001:db8::1"}
	instance := providerModel.Instance{PrivateIP: "10.0.0.2", PublicIP: "192.0.2.1", IPv6Address: "2001:db8::2/48", PublicIPv6: "2001:db8:1::2/128", NetworkType: "nat_ipv4"}
	for _, target := range []string{"10.0.0.2", "2001:db8::2", "2001:0db8:1:0:0:0:0:2"} {
		if err := validateInstanceTarget(instance, provider, target); err != nil {
			t.Errorf("owned address %s: %v", target, err)
		}
	}
	for _, target := range []string{"192.0.2.1", "2001:db8::1", "10.0.0.3", "2001:db8::3", "2001:db8:1::3", "127.0.0.1", "::1", "0.0.0.0", "::", "169.254.169.254", "ff02::1", "[2001:db8::2]"} {
		if err := validateInstanceTarget(instance, provider, target); err == nil {
			t.Errorf("accepted unowned/special address %s", target)
		}
	}
	instance.PublicIP = "192.0.2.22"
	if err := validateInstanceTarget(instance, provider, instance.PublicIP); err == nil {
		t.Fatal("accepted shared NAT PublicIP")
	}
	instance.NetworkType = "dedicated_ipv4"
	if err := validateInstanceTarget(instance, provider, instance.PublicIP); err != nil {
		t.Fatal(err)
	}
}

func TestDomainRestoreTargetAllowsOnlyReservedDestructiveStates(t *testing.T) {
	domain := &domainModel.Domain{
		UserID:     7,
		InstanceID: 11,
		ProviderID: 13,
		InternalIP: "10.0.0.2",
	}
	provider := providerModel.Provider{ID: 13}
	instance := providerModel.Instance{
		ID:         11,
		UserID:     7,
		ProviderID: 13,
		PrivateIP:  "10.0.0.2",
		Status:     "deleting",
	}
	if err := validateDomainRestoreTarget(domain, instance, provider, false); !errors.Is(err, errDomainInstanceBusy) {
		t.Fatalf("normal restore error = %v, want busy", err)
	}
	if err := validateDomainRestoreTarget(domain, instance, provider, true); err != nil {
		t.Fatalf("failed-delete restore rejected reserved instance: %v", err)
	}

	wrongTarget := instance
	wrongTarget.PrivateIP = "10.0.0.3"
	if err := validateDomainRestoreTarget(domain, wrongTarget, provider, true); err == nil {
		t.Fatal("failed-delete restore accepted an IP no longer owned by the instance")
	}
	wrongOwner := instance
	wrongOwner.UserID++
	if err := validateDomainRestoreTarget(domain, wrongOwner, provider, true); err == nil {
		t.Fatal("failed-delete restore accepted a changed instance owner")
	}

	instance.Status = "error"
	if err := validateDomainRestoreTarget(domain, instance, provider, true); err != nil {
		t.Fatalf("failed-delete restore rejected retained error instance: %v", err)
	}
}

func TestDomainTXTVerificationIsBoundToUserAndDomain(t *testing.T) {
	old := global.GetAppConfig()
	defer global.SetAppConfig(old)
	cfg := old
	cfg.JWT.SigningKey = "local-test-domain-signing-key"
	global.SetAppConfig(cfg)
	first, err := domainVerification(7, "App.Example.Test")
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []struct {
		user   uint
		domain string
	}{{8, "app.example.test"}, {7, "other.example.test"}} {
		claim, err := domainVerification(other.user, other.domain)
		if err != nil {
			t.Fatal(err)
		}
		if claim.Value == first.Value {
			t.Fatal("domain claim is reusable across owners")
		}
	}
	lookup := func(ctx context.Context, name string) ([]string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded DNS lookup")
		}
		if name != first.Name {
			t.Fatal(name)
		}
		return []string{first.Value}, nil
	}
	if err := verifyDomainOwnership(context.Background(), 7, "app.example.test", lookup); err != nil {
		t.Fatal(err)
	}
	if err := verifyDomainOwnership(context.Background(), 8, "app.example.test", lookup); err == nil {
		t.Fatal("accepted other user's TXT")
	}
}
