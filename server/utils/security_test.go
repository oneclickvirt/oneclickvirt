package utils

import (
	"net/http"
	"testing"
)

func TestShellSingleQuote(t *testing.T) {
	input := "name'; touch /tmp/pwned; echo '"
	want := "'name'\"'\"'; touch /tmp/pwned; echo '\"'\"''"
	if got := ShellSingleQuote(input); got != want {
		t.Fatalf("unexpected shell quote:\nwant: %s\n got: %s", want, got)
	}
	if got := ShellSingleQuote(""); got != "''" {
		t.Fatalf("empty string quote mismatch: %s", got)
	}
}

func TestOriginAllowedForRequest(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		host        string
		headers     map[string]string
		origin      string
		frontendURL string
		whitelist   []string
		want        bool
	}{
		{
			name:    "same public host",
			url:     "https://api.example.com/api/v1/test",
			host:    "api.example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			origin:  "https://api.example.com",
			want:    true,
		},
		{
			name:    "reverse proxy forwarded host",
			url:     "http://127.0.0.1:3000/api/v1/test",
			host:    "127.0.0.1:3000",
			headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "panel.example.com"},
			origin:  "https://panel.example.com",
			want:    true,
		},
		{
			name:    "reverse proxy forwarded port",
			url:     "http://127.0.0.1:3000/api/v1/test",
			host:    "127.0.0.1:3000",
			headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "panel.example.com", "X-Forwarded-Port": "8443"},
			origin:  "https://panel.example.com:8443",
			want:    true,
		},
		{
			name:    "rfc forwarded header",
			url:     "http://127.0.0.1:3000/api/v1/test",
			host:    "127.0.0.1:3000",
			headers: map[string]string{"Forwarded": `for=127.0.0.1;proto=https;host="panel.example.com"`},
			origin:  "https://panel.example.com",
			want:    true,
		},
		{
			name:   "localhost all in one",
			url:    "http://localhost:8888/api/v1/test",
			host:   "localhost:8888",
			origin: "http://localhost:8888",
			want:   true,
		},
		{
			name:        "configured frontend origin",
			url:         "https://api.example.com/api/v1/test",
			host:        "api.example.com",
			headers:     map[string]string{"X-Forwarded-Proto": "https"},
			origin:      "https://frontend.example.com",
			frontendURL: "https://frontend.example.com",
			want:        true,
		},
		{
			name:      "cors whitelist origin",
			url:       "https://api.example.com/api/v1/test",
			host:      "api.example.com",
			origin:    "https://panel2.example.com",
			whitelist: []string{"https://panel2.example.com"},
			want:      true,
		},
		{
			name:    "cross site rejected",
			url:     "https://api.example.com/api/v1/test",
			host:    "api.example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			origin:  "https://evil.example.net",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tt.host
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			if got := OriginAllowedForRequest(req, tt.origin, tt.frontendURL, tt.whitelist); got != tt.want {
				t.Fatalf("OriginAllowedForRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
