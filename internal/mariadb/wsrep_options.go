package mariadb

import (
	"fmt"
	"strings"
)

const defaultTLSWsrepOptions = "pc.wait_prim=FALSE;gcache.recover=no;gmcast.listen_addr=tcp://{ PODIP }:4567;" +
	"socket.ssl_key=/etc/pki/tls/private/galera.key;" +
	"socket.ssl_cert=/etc/pki/tls/certs/galera.crt;" +
	"socket.ssl_cipher={ SSL_CIPHER };" +
	"socket.ssl_ca=/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem;"

// MergedTLSWsrepOptions merges wsrep_provider_options from customServiceConfig into operator TLS defaults.
// Operator socket.ssl_* values always win.
func MergedTLSWsrepOptions(customINI string) (string, error) {
	opts, err := parseSemicolonOptions(defaultTLSWsrepOptions)
	if err != nil {
		return "", err
	}
	userVal, ok := wsrepFromINI(customINI)
	if !ok {
		return joinSemicolonOptions(opts), nil
	}
	user, err := parseSemicolonOptions(userVal)
	if err != nil {
		return "", err
	}
	for k, v := range user {
		if !strings.HasPrefix(k, "socket.ssl_") {
			opts[k] = v
		}
	}
	return joinSemicolonOptions(opts), nil
}

// ApplyMergedWsrepToCustomConfig replaces wsrep_provider_options in customServiceConfig with the merged
// value when present. Config files load as 10-galera, 20-galera_tls, 30-galera_custom; the custom file
// must carry the final wsrep_provider_options scalar when the user sets it.
func ApplyMergedWsrepToCustomConfig(customINI string) (string, error) {
	if _, ok := wsrepFromINI(customINI); !ok {
		return customINI, nil
	}
	merged, err := MergedTLSWsrepOptions(customINI)
	if err != nil {
		return "", err
	}
	return setWsrepInINI(customINI, merged), nil
}

func setWsrepInINI(ini, merged string) string {
	lines := strings.Split(ini, "\n")
	inMysqld := false
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			inMysqld = strings.EqualFold(section, "mysqld")
			continue
		}
		if !inMysqld {
			continue
		}
		key, _, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(key) == "wsrep_provider_options" {
			lines[i] = "wsrep_provider_options = " + merged
			replaced = true
		}
	}
	if !replaced {
		return ini
	}
	return strings.Join(lines, "\n")
}

func wsrepFromINI(ini string) (string, bool) {
	inMysqld := false
	for _, line := range strings.Split(ini, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			inMysqld = strings.EqualFold(section, "mysqld")
			continue
		}
		if !inMysqld {
			continue
		}
		key, val, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(key) == "wsrep_provider_options" {
			return strings.TrimSpace(val), true
		}
	}
	return "", false
}

func parseSemicolonOptions(value string) (map[string]string, error) {
	out := make(map[string]string)
	for _, token := range strings.Split(value, ";") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		key, val, ok := strings.Cut(token, "=")
		if !ok {
			return nil, fmt.Errorf("invalid wsrep_provider_options token %q", token)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid wsrep_provider_options token %q", token)
		}
		out[key] = strings.TrimSpace(val)
	}
	return out, nil
}

func joinSemicolonOptions(opts map[string]string) string {
	parts := make([]string, 0, len(opts))
	for k, v := range opts {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ";") + ";"
}
