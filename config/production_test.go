package config

import (
	"strings"
	"testing"
)

const goodSecret = "0123456789abcdef0123456789abcdef-secret"

// productionEnv sets a configuration that passes every check, then applies changes.
func productionEnv(t *testing.T, changes map[string]string) {
	t.Helper()
	env := map[string]string{
		"APP_ENV": "production", "JWT_ACCESS_SECRET": goodSecret + "-a", "JWT_REFRESH_SECRET": goodSecret + "-r",
		"PASSWORD_RESET_PEPPER": goodSecret + "-p", "AI_ENCRYPTION_KEY": strings.Repeat("ab", 32),
		"CORS_ALLOWED_ORIGINS": "https://app.example.com", "DB_SSLMODE": "require",
		"CLOUD_EXECUTION_ENABLED": "", "ALLOW_PUBLIC_CODE_EXECUTION": "", "TRUSTED_PROXIES": "", "ALLOW_PLAINTEXT_DB": "",
	}
	for name, value := range changes {
		env[name] = value
	}
	for name, value := range env {
		t.Setenv(name, value)
	}
}

func TestACorrectProductionConfigurationHasNoProblems(t *testing.T) {
	productionEnv(t, nil)
	if problems := ProductionProblems(); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestProductionRefusesEachUnsafeSetting(t *testing.T) {
	cases := map[string]struct {
		change map[string]string
		want   string
	}{
		"missing access secret": {map[string]string{"JWT_ACCESS_SECRET": ""}, "JWT_ACCESS_SECRET"},
		"short access secret":   {map[string]string{"JWT_ACCESS_SECRET": "short"}, "JWT_ACCESS_SECRET"},
		"short refresh secret":  {map[string]string{"JWT_REFRESH_SECRET": "short"}, "JWT_REFRESH_SECRET"},
		"same secrets":          {map[string]string{"JWT_REFRESH_SECRET": goodSecret + "-a"}, "must be different"},
		"no pepper":             {map[string]string{"PASSWORD_RESET_PEPPER": ""}, "PASSWORD_RESET_PEPPER"},
		"no encryption key":     {map[string]string{"AI_ENCRYPTION_KEY": ""}, "AI_ENCRYPTION_KEY"},
		"no origins":            {map[string]string{"CORS_ALLOWED_ORIGINS": ""}, "CORS_ALLOWED_ORIGINS"},
		"wildcard origin":       {map[string]string{"CORS_ALLOWED_ORIGINS": "*"}, "CORS_ALLOWED_ORIGINS"},
		"http origin":           {map[string]string{"CORS_ALLOWED_ORIGINS": "http://app.example.com"}, "https"},
		"database without tls":  {map[string]string{"DB_SSLMODE": "disable"}, "DB_SSLMODE"},
		"database tls unset":    {map[string]string{"DB_SSLMODE": ""}, "DB_SSLMODE"},
		"database tls prefer":   {map[string]string{"DB_SSLMODE": "prefer"}, "DB_SSLMODE"},
		"public code execution": {map[string]string{"CLOUD_EXECUTION_ENABLED": "true"}, "ALLOW_PUBLIC_CODE_EXECUTION"},
	}
	for name, test := range cases {
		productionEnv(t, test.change)
		problems := strings.Join(ProductionProblems(), "\n")
		if !strings.Contains(problems, test.want) {
			t.Errorf("%s: problems = %q, want mention of %q", name, problems, test.want)
		}
	}
	// A database on the same private network may be plaintext, but only on purpose.
	productionEnv(t, map[string]string{"DB_SSLMODE": "disable", "ALLOW_PLAINTEXT_DB": "true"})
	if problems := ProductionProblems(); len(problems) != 0 {
		t.Errorf("the explicit plaintext opt-in was refused: %v", problems)
	}
	productionEnv(t, map[string]string{"DB_SSLMODE": "disable", "ALLOW_PLAINTEXT_DB": "yes"})
	if problems := ProductionProblems(); len(problems) == 0 {
		t.Error("anything but exactly true must not opt in")
	}
	// Opting in to public execution is possible, but only on purpose.
	productionEnv(t, map[string]string{"CLOUD_EXECUTION_ENABLED": "true", "ALLOW_PUBLIC_CODE_EXECUTION": "true"})
	if problems := ProductionProblems(); len(problems) != 0 {
		t.Errorf("the explicit opt-in was refused: %v", problems)
	}
}

func TestProblemsNameVariablesButNeverShowSecrets(t *testing.T) {
	productionEnv(t, map[string]string{"JWT_ACCESS_SECRET": "tooshort-VERYSECRETVALUE"})
	for _, problem := range ProductionProblems() {
		if strings.Contains(problem, "VERYSECRETVALUE") {
			t.Fatalf("a secret reached the message: %s", problem)
		}
	}
}

func TestCORSOriginsDefaultsOnlyOutsideProduction(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	if origins := CORSOrigins(); len(origins) == 0 || !strings.HasPrefix(origins[0], "http://localhost") {
		t.Fatalf("development default = %v", origins)
	}
	t.Setenv("APP_ENV", "production")
	if origins := CORSOrigins(); len(origins) != 0 {
		t.Fatalf("production with no list allowed %v", origins)
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.com, *")
	if origins := CORSOrigins(); len(origins) != 1 || origins[0] != "https://app.example.com" {
		t.Fatalf("origins = %v", origins)
	}
}

func TestTrustedProxiesAndSSLModeDefaults(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	if len(TrustedProxies()) != 0 {
		t.Error("a proxy is trusted by default")
	}
	t.Setenv("TRUSTED_PROXIES", " 10.0.0.1 , 172.16.0.0/12,, ")
	if got := TrustedProxies(); len(got) != 2 || got[1] != "172.16.0.0/12" {
		t.Errorf("proxies = %v", got)
	}
	t.Setenv("DB_SSLMODE", "")
	if DatabaseSSLMode() != "disable" {
		t.Error("development default changed")
	}
	t.Setenv("DB_SSLMODE", "verify-full")
	if DatabaseSSLMode() != "verify-full" {
		t.Error("the setting is ignored")
	}
}

func TestJWTSecretsAreRequiredInEveryMode(t *testing.T) {
	for _, mode := range []string{"", "development", "production"} {
		t.Setenv("APP_ENV", mode)
		t.Setenv("JWT_ACCESS_SECRET", "")
		t.Setenv("JWT_REFRESH_SECRET", "")
		if problems := AlwaysRequiredProblems(); len(problems) != 2 {
			t.Fatalf("APP_ENV=%q with no JWT keys: problems = %v", mode, problems)
		}
	}
	t.Setenv("JWT_ACCESS_SECRET", "same-value")
	t.Setenv("JWT_REFRESH_SECRET", "same-value")
	if problems := AlwaysRequiredProblems(); len(problems) != 1 || !strings.Contains(problems[0], "different") {
		t.Fatalf("equal keys: problems = %v", problems)
	}
	t.Setenv("JWT_REFRESH_SECRET", "other-value")
	if problems := AlwaysRequiredProblems(); len(problems) != 0 {
		t.Fatalf("set keys: problems = %v", problems)
	}
	for _, problem := range AlwaysRequiredProblems() {
		if strings.Contains(problem, "same-value") || strings.Contains(problem, "other-value") {
			t.Fatalf("a secret reached the message: %s", problem)
		}
	}
}
