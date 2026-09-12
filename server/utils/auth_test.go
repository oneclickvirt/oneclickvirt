package utils

import (
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTokenIssuedAt(t *testing.T) {
	issued := time.Unix(1700000000, 123456789)
	for _, tc := range []struct {
		name    string
		claims  jwt.MapClaims
		want    time.Time
		invalid bool
	}{
		{"precise", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": strconv.FormatInt(issued.UnixNano(), 10)}, issued, false},
		{"legacy", jwt.MapClaims{"iat": float64(issued.Unix())}, issued.Truncate(time.Second), false},
		{"missing iat", jwt.MapClaims{"iat_ns": strconv.FormatInt(issued.UnixNano(), 10)}, time.Time{}, true},
		{"invalid iat", jwt.MapClaims{"iat": "bad"}, time.Time{}, true},
		{"zero iat", jwt.MapClaims{"iat": float64(0)}, time.Time{}, true},
		{"numeric nanos", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": float64(issued.UnixNano())}, time.Time{}, true},
		{"null nanos", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": nil}, time.Time{}, true},
		{"empty nanos", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": ""}, time.Time{}, true},
		{"overflow nanos", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": "9223372036854775808"}, time.Time{}, true},
		{"negative nanos", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": "-1"}, time.Time{}, true},
		{"inconsistent nanos", jwt.MapClaims{"iat": float64(issued.Unix()), "iat_ns": strconv.FormatInt(issued.Add(time.Second).UnixNano(), 10)}, time.Time{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TokenIssuedAt(&tc.claims)
			if (err != nil) != tc.invalid || (!tc.invalid && !got.Equal(tc.want)) {
				t.Fatalf("got %s, error %v; want %s, invalid %v", got, err, tc.want, tc.invalid)
			}
		})
	}
	if _, err := TokenIssuedAt(nil); err == nil {
		t.Fatal("nil claims must be rejected")
	}
}

func TestGenerateTokenPreservesPreciseIssuedAt(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", "local-test-signing-key")
	before := time.Now()
	token, err := GenerateToken(1, "test", "user")
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ValidateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := (*claims)["iat_ns"].(string); !ok {
		t.Fatal("issuer must include signed string iat_ns")
	}
	issued, err := TokenIssuedAt(claims)
	if err != nil || issued.Before(before) || issued.After(after) {
		t.Fatalf("issued %s outside [%s, %s], error %v", issued, before, after, err)
	}
}
