package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFileLiteralValuesAndPriority(t *testing.T) {
	const key = "POC_TEST_ENV_VALUE"
	t.Setenv(key, "from-environment")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# local\nexport "+key+"='from-file'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "from-environment" {
		t.Fatal("environnement écrasé")
	}
	os.Unsetenv(key)
	if err := os.WriteFile(path, []byte(key+"=\"literal$()#value\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "literal$()#value" {
		t.Fatal("valeur non littérale")
	}
}
func TestEnvFileErrorsNeverEchoValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("BAD-NAME=secret-never-log\n"), 0600)
	err := loadEnvFile(path)
	if err == nil || strings.Contains(err.Error(), "secret-never-log") {
		t.Fatal("erreur de configuration incorrecte")
	}
}
