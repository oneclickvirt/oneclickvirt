package admin

import "testing"

func TestAcceleratorMergeKeyNormalizesBusAcrossSources(t *testing.T) {
	first := map[string]string{
		"kind":   "gpu",
		"id":     "0",
		"name":   "NVIDIA GeForce RTX 4090",
		"vendor": "NVIDIA",
		"bus":    "00000000:17:00.0",
		"source": "nvidia-smi",
	}
	second := map[string]string{
		"kind":   "gpu",
		"name":   "NVIDIA Corporation AD102 [GeForce RTX 4090]",
		"vendor": "NVIDIA",
		"bus":    "0000:17:00.0",
		"source": "lspci",
	}

	if acceleratorMergeKey(first) != acceleratorMergeKey(second) {
		t.Fatalf("expected identical merge key for same GPU bus, got %q and %q", acceleratorMergeKey(first), acceleratorMergeKey(second))
	}
}

func TestMergeAcceleratorRecordPrefersHigherQualitySource(t *testing.T) {
	dst := map[string]string{
		"kind":   "gpu",
		"name":   "NVIDIA Corporation AD102 [GeForce RTX 4090]",
		"vendor": "NVIDIA",
		"bus":    "0000:17:00.0",
		"source": "lspci",
	}
	src := map[string]string{
		"kind":   "gpu",
		"id":     "0",
		"name":   "NVIDIA GeForce RTX 4090",
		"vendor": "NVIDIA",
		"bus":    "00000000:17:00.0",
		"source": "nvidia-smi",
	}

	mergeAcceleratorRecord(dst, src)

	if dst["id"] != "0" {
		t.Fatalf("expected merged device to keep detected id, got %q", dst["id"])
	}
	if dst["source"] != "nvidia-smi" {
		t.Fatalf("expected higher quality source to win, got %q", dst["source"])
	}
	if normalizePCIBus(dst["bus"]) != "0000:17:00.0" {
		t.Fatalf("expected normalized bus to remain stable, got %q", dst["bus"])
	}
}
