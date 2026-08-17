package traffic

import "testing"

func TestTrafficDataSourceFromMethods(t *testing.T) {
	tests := []struct {
		name    string
		methods []string
		want    string
	}{
		{name: "no enabled provider", methods: nil, want: trafficDataSourceNone},
		{name: "agent", methods: []string{"agent"}, want: trafficDataSourceAgent},
		{name: "legacy blank is pmacct", methods: []string{""}, want: trafficDataSourcePmacct},
		{name: "pmacct", methods: []string{"pmacct"}, want: trafficDataSourcePmacct},
		{name: "unknown method is not mislabelled", methods: []string{"unknown"}, want: trafficDataSourceNone},
		{name: "mixed", methods: []string{"agent", "pmacct", "agent"}, want: trafficDataSourceMixed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trafficDataSourceFromMethods(test.methods); got != test.want {
				t.Fatalf("trafficDataSourceFromMethods(%v) = %q, want %q", test.methods, got, test.want)
			}
		})
	}
}

func TestDefaultTrafficDataSourcesAlwaysUsesExplicitNone(t *testing.T) {
	sources := defaultTrafficDataSources([]uint{9, 0, 3, 9})
	if len(sources) != 2 || sources[3] != trafficDataSourceNone || sources[9] != trafficDataSourceNone {
		t.Fatalf("defaultTrafficDataSources() = %#v, want explicit none for each valid user", sources)
	}
}
