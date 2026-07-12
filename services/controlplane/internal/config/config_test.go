package config

import "testing"

func TestValidateSessionSecret(t *testing.T) {
	cases := []struct {
		name       string
		env        string
		secret     string
		wantErr    bool
	}{
		{"dev allows default", "dev", insecureSessionSecret, false},
		{"prod rejects default", "production", insecureSessionSecret, true},
		{"prod rejects env-example placeholder", "production", "change-me-to-a-long-random-string", true},
		{"prod rejects short", "production", "short", true},
		{"prod rejects empty", "production", "", true},
		{"prod accepts strong", "production", "a3f9c1e2b7d4859061f2a3b4c5d6e7f8", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{Env: c.env, SessionSecret: c.secret}
			if err := cfg.Validate(); (err != nil) != c.wantErr {
				t.Fatalf("Validate()=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}
