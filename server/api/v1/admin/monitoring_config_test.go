package admin

import "testing"

func intPtr(value int) *int          { return &value }
func stringPtr(value string) *string { return &value }

func TestUpdateMonitoringConfigRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     UpdateMonitoringConfigRequest
		wantErr bool
	}{
		{name: "omitted fields are valid", req: UpdateMonitoringConfigRequest{}},
		{name: "pmacct mode is valid", req: UpdateMonitoringConfigRequest{MonitoringMode: stringPtr("pmacct")}},
		{name: "valid values", req: UpdateMonitoringConfigRequest{
			MonitoringMode:          stringPtr("agent"),
			AgentPort:               intPtr(23782),
			CollectInterval:         intPtr(5),
			ResourceCollectInterval: intPtr(30),
			TrafficCollectMethod:    stringPtr("nft"),
		}},
		{name: "explicit zero traffic interval", req: UpdateMonitoringConfigRequest{CollectInterval: intPtr(0)}, wantErr: true},
		{name: "explicit zero resource interval", req: UpdateMonitoringConfigRequest{ResourceCollectInterval: intPtr(0)}, wantErr: true},
		{name: "resource interval below agent minimum", req: UpdateMonitoringConfigRequest{ResourceCollectInterval: intPtr(9)}, wantErr: true},
		{name: "invalid mode", req: UpdateMonitoringConfigRequest{MonitoringMode: stringPtr("invalid")}, wantErr: true},
		{name: "legacy passive mode is invalid", req: UpdateMonitoringConfigRequest{MonitoringMode: stringPtr("passive")}, wantErr: true},
		{name: "invalid collection method", req: UpdateMonitoringConfigRequest{TrafficCollectMethod: stringPtr("invalid")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
