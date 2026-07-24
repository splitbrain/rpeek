// Package conf resolves rpeek's connection settings. It is shared by the client and the
// server so both honour the same env var names and the same flag > env > default precedence.
package conf

import "os"

// Environment variable names read for a connection setting when its flag is unset.
const (
	// EnvHost is the environment variable holding the server address.
	EnvHost = "RPEEK_HOST"
	// EnvToken is the environment variable holding the auth token.
	EnvToken = "RPEEK_TOKEN"
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
