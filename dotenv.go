package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadEnvFile supports literal KEY=value lines and optional matching quotes.
// The process environment wins. No shell execution or variable expansion occurs.
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" {
			return fmt.Errorf(".env ligne %d : KEY=value attendu", line)
		}
		for i, r := range key {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9')) {
				return fmt.Errorf(".env ligne %d : nom invalide", line)
			}
		}
		if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
			if len(value) < 2 || value[len(value)-1] != value[0] {
				return fmt.Errorf(".env ligne %d : guillemets non fermés", line)
			}
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf(".env ligne %d : variable invalide", line)
			}
		}
	}
	if scanner.Err() != nil {
		return fmt.Errorf("lecture .env impossible")
	}
	return nil
}
