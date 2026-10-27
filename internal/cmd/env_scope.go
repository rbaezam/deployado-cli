package cmd

import (
	"fmt"
	"strings"

	"github.com/deployado/cli/internal/api"
)

var buildScopeEnvPrefixes = []string{
	"VITE_",
	"NEXT_PUBLIC_",
	"REACT_APP_",
	"NUXT_PUBLIC_",
	"VUE_APP_",
}

func envScopeForKey(key string) string {
	upperKey := strings.ToUpper(strings.TrimSpace(key))

	for _, prefix := range buildScopeEnvPrefixes {
		if strings.HasPrefix(upperKey, prefix) {
			return "build"
		}
	}

	return "runtime"
}

func splitEnvByScope(env map[string]string) (runtimeEnv map[string]string, buildEnv map[string]string) {
	runtimeEnv = make(map[string]string)
	buildEnv = make(map[string]string)

	for key, value := range env {
		if envScopeForKey(key) == "build" {
			buildEnv[key] = value
		} else {
			runtimeEnv[key] = value
		}
	}

	return runtimeEnv, buildEnv
}

func setEnvWithAutoScope(client *api.Client, appID string, env map[string]string) (int, int, error) {
	runtimeEnv, buildEnv := splitEnvByScope(env)

	if len(buildEnv) > 0 {
		if err := client.SetEnvWithScope(appID, buildEnv, "build"); err != nil {
			return len(runtimeEnv), len(buildEnv), fmt.Errorf("failed to set build-scope env vars: %w", err)
		}
	}

	if len(runtimeEnv) > 0 {
		if err := client.SetEnvWithScope(appID, runtimeEnv, "runtime"); err != nil {
			return len(runtimeEnv), len(buildEnv), fmt.Errorf("failed to set runtime-scope env vars: %w", err)
		}
	}

	return len(runtimeEnv), len(buildEnv), nil
}
