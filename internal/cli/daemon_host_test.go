package cli

import (
	"strings"
	"testing"

	"github.com/CoreyRDean/intent/internal/config"
)

func TestNormalizeLocalDaemonHost(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "default empty host", raw: "", want: "127.0.0.1"},
		{name: "localhost", raw: "localhost", want: "127.0.0.1"},
		{name: "ipv4 loopback", raw: "127.0.0.1", want: "127.0.0.1"},
		{name: "ipv6 loopback", raw: "::1", want: "127.0.0.1"},
		{name: "bracketed ipv6 loopback", raw: "[::1]", want: "127.0.0.1"},
		{name: "non-loopback wildcard rejected", raw: "0.0.0.0", wantErr: "loopback only"},
		{name: "non-loopback ip rejected", raw: "192.168.1.10", wantErr: "loopback only"},
		{name: "hostname rejected", raw: "example.com", wantErr: "loopback only"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLocalDaemonHost(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("host = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLocalDaemonEndpoint(t *testing.T) {
	cfg := &config.Config{Raw: map[string]string{
		"daemon.host": " localhost ",
		"daemon.port": " 19090 ",
	}}

	host, port, err := resolveLocalDaemonEndpoint(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %q, want %q", host, "127.0.0.1")
	}
	if port != "19090" {
		t.Fatalf("port = %q, want %q", port, "19090")
	}
}

func TestValidateConfigValueRejectsRemoteDaemonHost(t *testing.T) {
	err := validateConfigValue("daemon.host", "0.0.0.0")
	if err == nil {
		t.Fatal("expected daemon.host validation error, got nil")
	}
	if !strings.Contains(err.Error(), "loopback only") {
		t.Fatalf("error = %q, want loopback hint", err)
	}
}
