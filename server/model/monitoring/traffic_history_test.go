package monitoring

import (
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestAggregateTrafficHistoryModelsHaveUniquePeriodIndexes(t *testing.T) {
	tests := []struct {
		name      string
		model     interface{}
		indexName string
		fields    []string
	}{
		{
			name:      "provider",
			model:     &ProviderTrafficHistory{},
			indexName: "uk_provider_period",
			fields:    []string{"provider_id", "year", "month", "day", "hour"},
		},
		{
			name:      "user",
			model:     &UserTrafficHistory{},
			indexName: "uk_user_period",
			fields:    []string{"user_id", "year", "month", "day", "hour"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := schema.Parse(tt.model, &sync.Map{}, schema.NamingStrategy{})
			if err != nil {
				t.Fatalf("parse schema: %v", err)
			}

			for _, index := range parsed.ParseIndexes() {
				if index.Name != tt.indexName {
					continue
				}
				if index.Class != "UNIQUE" {
					t.Fatalf("index %s class = %q, want UNIQUE", tt.indexName, index.Class)
				}
				fields := make([]string, 0, len(index.Fields))
				for _, field := range index.Fields {
					fields = append(fields, field.Field.DBName)
				}
				if !reflect.DeepEqual(fields, tt.fields) {
					t.Fatalf("index %s fields = %v, want %v", tt.indexName, fields, tt.fields)
				}
				return
			}

			t.Fatalf("missing unique index %s", tt.indexName)
		})
	}
}
