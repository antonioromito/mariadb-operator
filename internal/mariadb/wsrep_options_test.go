package mariadb

import (
	"strings"
	"testing"
)

func TestApplyMergedWsrepToCustomConfig(t *testing.T) {
	ini := "[mysqld]\nwsrep_provider_options = gcache.size=4G\nmax_connections = 8192\n"
	out, err := ApplyMergedWsrepToCustomConfig(ini)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "gcache.size=4G") {
		t.Fatalf("missing user tunable: %s", out)
	}
	if !strings.Contains(out, "socket.ssl_key=/etc/pki/tls/private/galera.key") {
		t.Fatalf("missing TLS defaults: %s", out)
	}
	// Merged value is a single line; must not be only the user's partial wsrep string.
	if strings.TrimSpace(strings.Split(out, "\n")[1]) == "wsrep_provider_options = gcache.size=4G" {
		t.Fatalf("unmerged wsrep line must be replaced: %s", out)
	}
}

func TestApplyMergedWsrepToCustomConfig_noWsrepUnchanged(t *testing.T) {
	ini := "[mysqld]\nmax_connections = 8192\n"
	out, err := ApplyMergedWsrepToCustomConfig(ini)
	if err != nil {
		t.Fatal(err)
	}
	if out != ini {
		t.Fatalf("expected unchanged config, got %q", out)
	}
}

func TestApplyMergedWsrepToCustomConfig_userTLSOverridden(t *testing.T) {
	ini := "[mysqld]\nwsrep_provider_options = socket.ssl_key=/tmp/evil.key\n"
	out, err := ApplyMergedWsrepToCustomConfig(ini)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "/tmp/evil.key") {
		t.Fatalf("user socket.ssl_key must not win: %s", out)
	}
}
