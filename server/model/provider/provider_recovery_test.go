package provider

import "testing"

func TestInstanceBeforeCreateSetsConservativeRecoveryIntent(t *testing.T) {
	tests := []struct {
		name string
		in   Instance
		want string
	}{
		{
			name: "normal creation is expected to run",
			in:   Instance{Status: "creating"},
			want: InstanceDesiredStateRunning,
		},
		{
			name: "running imported instance stays opt in",
			in:   Instance{Status: "running", IsImported: true},
			want: InstanceDesiredStateStopped,
		},
		{
			name: "explicit manual stop is preserved",
			in:   Instance{Status: "running", DesiredState: InstanceDesiredStateStopped},
			want: InstanceDesiredStateStopped,
		},
		{
			name: "explicit run intent is normalized",
			in:   Instance{Status: "stopped", DesiredState: " RUNNING "},
			want: InstanceDesiredStateRunning,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := test.in
			if err := instance.BeforeCreate(nil); err != nil {
				t.Fatalf("BeforeCreate() error = %v", err)
			}
			if instance.DesiredState != test.want {
				t.Fatalf("DesiredState = %q, want %q", instance.DesiredState, test.want)
			}
		})
	}
}
