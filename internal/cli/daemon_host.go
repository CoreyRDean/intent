package cli

import (
	"fmt"
	"net"
	"strings"

	"github.com/CoreyRDean/intent/internal/config"
)

const defaultLocalDaemonHost = "127.0.0.1"
const defaultLocalDaemonPort = "18080"

// normalizeLocalDaemonHost accepts only loopback hosts for the local daemon.
// Any accepted value is canonicalized to 127.0.0.1 so the local backend never
// accidentally exposes the model server on a broader interface.
func normalizeLocalDaemonHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		return defaultLocalDaemonHost, nil
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if strings.EqualFold(host, "localhost") {
		return defaultLocalDaemonHost, nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return defaultLocalDaemonHost, nil
	}
	return "", fmt.Errorf("daemon.host %q must resolve to loopback only", strings.TrimSpace(raw))
}

func resolveLocalDaemonHost(cfg *config.Config) (string, error) {
	if cfg == nil {
		return normalizeLocalDaemonHost("")
	}
	return normalizeLocalDaemonHost(cfg.Raw["daemon.host"])
}

func resolveLocalDaemonPort(cfg *config.Config) string {
	if cfg == nil {
		return defaultLocalDaemonPort
	}
	if port := strings.TrimSpace(cfg.Raw["daemon.port"]); port != "" {
		return port
	}
	return defaultLocalDaemonPort
}

func resolveLocalDaemonEndpoint(cfg *config.Config) (host, port string, err error) {
	host, err = resolveLocalDaemonHost(cfg)
	if err != nil {
		return "", "", err
	}
	return host, resolveLocalDaemonPort(cfg), nil
}
