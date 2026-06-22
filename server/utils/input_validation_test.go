package utils

import "testing"

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{name: "valid ascii", username: "user_123", wantErr: false},
		{name: "valid unicode", username: "测试用户", wantErr: false},
		{name: "reject html", username: "<script>alert(1)</script>", wantErr: true},
		{name: "reject sql", username: "test; DROP TABLE users;--", wantErr: true},
		{name: "reject whitespace padding", username: " user ", wantErr: true},
		{name: "reject too short", username: "ab", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.username)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateUsername(%q) error = %v, wantErr %v", tt.username, err, tt.wantErr)
			}
		})
	}
}

func TestValidateOptionalEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{name: "empty allowed", email: "", wantErr: false},
		{name: "valid email", email: "user@example.com", wantErr: false},
		{name: "invalid email", email: "not_an_email", wantErr: true},
		{name: "display name rejected", email: "User <user@example.com>", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOptionalEmail(tt.email)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateOptionalEmail(%q) error = %v, wantErr %v", tt.email, err, tt.wantErr)
			}
		})
	}
}

func TestIsValidLXDInstanceName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "hyphen name", in: "type-test-ct", want: true},
		{name: "underscore rejected", in: "type_test_ct", want: false},
		{name: "leading hyphen rejected", in: "-bad", want: false},
		{name: "trailing hyphen rejected", in: "bad-", want: false},
		{name: "double hyphen rejected", in: "bad--name", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidLXDInstanceName(tt.in); got != tt.want {
				t.Fatalf("IsValidLXDInstanceName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
