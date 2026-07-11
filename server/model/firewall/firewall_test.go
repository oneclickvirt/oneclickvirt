package firewall

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestBlockRuleApplicationHasUniqueTargetIndex(t *testing.T) {
	parsed, err := schema.Parse(&BlockRuleApplication{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	wantFields := []string{"rule_id", "scope", "target_id"}
	for _, index := range parsed.ParseIndexes() {
		if index.Name != "uk_bra_target" {
			continue
		}
		if index.Class != "UNIQUE" {
			t.Fatalf("index class = %q, want UNIQUE", index.Class)
		}
		fields := make([]string, 0, len(index.Fields))
		for _, field := range index.Fields {
			fields = append(fields, field.Field.DBName)
		}
		if !reflect.DeepEqual(fields, wantFields) {
			t.Fatalf("index fields = %v, want %v", fields, wantFields)
		}
		return
	}

	t.Fatal("missing unique index uk_bra_target")
}
