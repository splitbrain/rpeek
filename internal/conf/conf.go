// Package conf resolves rpeek's connection settings. It is shared by the client and the
// server so both honour the same env var names and the same flag > env > default precedence.
package conf

import (
	"os"
	"strings"
)

// Environment variable names read for a connection setting when its flag is unset.
const (
	// EnvHost is the environment variable holding the server address.
	EnvHost = "RPEEK_HOST"
	// EnvToken is the environment variable holding the auth token.
	EnvToken = "RPEEK_TOKEN"
	// EnvDBPrefix prefixes the per-database DSN variables, RPEEK_DB_<ALIAS>. The DSN holds
	// the password, so this env form keeps it out of argv where ps would expose it.
	EnvDBPrefix = "RPEEK_DB_"
)

// Resolve returns a setting by the precedence flag > environment > default: flagVal if
// non-empty, else the value of environment variable envKey if non-empty, else def.
func Resolve(flagVal, envKey, def string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv(envKey); env != "" {
		return env
	}
	return def
}

// DBSpecs returns the alias→DSN pairs configured through RPEEK_DB_<ALIAS> environment
// variables. The alias is the variable's suffix, lower-cased, so RPEEK_DB_APP configures
// the alias "app".
func DBSpecs() map[string]string {
	specs := map[string]string{}
	for _, kv := range os.Environ() {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || val == "" {
			continue
		}
		if !strings.HasPrefix(key, EnvDBPrefix) {
			continue
		}
		alias := strings.ToLower(strings.TrimPrefix(key, EnvDBPrefix))
		if alias == "" {
			continue
		}
		specs[alias] = val
	}
	return specs
}
