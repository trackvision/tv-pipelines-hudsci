package env

import (
	"fmt"
	"os"

	"github.com/caarlos0/env/v9"
	_ "github.com/joho/godotenv/autoload"
	"github.com/trackvision/tv-shared-go/logger"
	"go.uber.org/zap"
)

func GetVars[T any]() (t *T) {
	vars := new(T)
	err := env.Parse(vars)
	if err != nil {
		panic("Couldn't retrieve env vars")
	}
	logger.Info("Retrieved env", zap.Any("vars", vars))
	return vars
}

func GetSecret(key string) (string, error) {
	secretB, err := os.ReadFile(fmt.Sprintf("/%s/value", key))
	if err == nil && len(secretB) > 0 {
		return string(secretB), nil
	}

	// ? should we just use mounts
	// in some cases (locally, for tests) its easier to define env variable
	// ! atm following services/libs depend on this: tv-resolver (tests), tv-shared-go/directus (tests)
	if secret, exists := os.LookupEnv(key); exists {
		return secret, nil
	}

	return "", fmt.Errorf("%s secret is not set", key)
}
