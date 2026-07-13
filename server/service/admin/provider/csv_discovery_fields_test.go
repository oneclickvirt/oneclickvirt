package provider

import (
	"testing"

	"oneclickvirt/model/admin"
)

func TestApplyExtendedCSVDiscoveryFlags(t *testing.T) {
	values := map[string]string{
		"instanceDiscoveryEnabled": "true",
		"discoveryAutoImport":      "false",
		"discoveryAutoAdjust":      "true",
	}
	createReq := admin.CreateProviderRequest{}
	if err := applyExtendedCSVToCreateReq(&createReq, values); err != nil {
		t.Fatal(err)
	}
	if !createReq.DiscoverMode || createReq.AutoImport || !createReq.AutoAdjustQuota {
		t.Fatalf("create discovery flags not restored: %#v", createReq)
	}

	updateReq := admin.UpdateProviderRequest{}
	if err := applyExtendedCSVToUpdateReq(&updateReq, values); err != nil {
		t.Fatal(err)
	}
	if updateReq.DiscoverMode == nil || !*updateReq.DiscoverMode ||
		updateReq.AutoImport == nil || *updateReq.AutoImport ||
		updateReq.AutoAdjustQuota == nil || !*updateReq.AutoAdjustQuota {
		t.Fatalf("update discovery flags not restored: %#v", updateReq)
	}
}
