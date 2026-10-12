package main

import "testing"

func TestStorageConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantOn  bool
		wantErr bool
		want    storageConfig
	}{
		{
			name:   "disabled without endpoint",
			env:    map[string]string{},
			wantOn: false,
		},
		{
			name: "http endpoint uses defaults",
			env: map[string]string{
				"RUSTFS_ENDPOINT":   "http://rustfs:9000",
				"RUSTFS_ACCESS_KEY": "clinic",
				"RUSTFS_SECRET_KEY": "secret",
			},
			wantOn: true,
			want: storageConfig{
				endpoint:  "rustfs:9000",
				accessKey: "clinic",
				secretKey: "secret",
				region:    "us-east-1",
				bucket:    "clinic",
				secure:    false,
			},
		},
		{
			name: "https endpoint and overrides",
			env: map[string]string{
				"RUSTFS_ENDPOINT":   "https://s3.example.com",
				"RUSTFS_ACCESS_KEY": "key",
				"RUSTFS_SECRET_KEY": "secret",
				"RUSTFS_REGION":     "eu-central-1",
				"RUSTFS_BUCKET":     "attachments",
			},
			wantOn: true,
			want: storageConfig{
				endpoint:  "s3.example.com",
				accessKey: "key",
				secretKey: "secret",
				region:    "eu-central-1",
				bucket:    "attachments",
				secure:    true,
			},
		},
		{
			name: "missing credentials",
			env: map[string]string{
				"RUSTFS_ENDPOINT": "http://rustfs:9000",
			},
			wantErr: true,
		},
		{
			name: "endpoint without host",
			env: map[string]string{
				"RUSTFS_ENDPOINT":   "rustfs",
				"RUSTFS_ACCESS_KEY": "clinic",
				"RUSTFS_SECRET_KEY": "secret",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear every key first so cases do not leak into each other.
			for _, key := range []string{"RUSTFS_ENDPOINT", "RUSTFS_ACCESS_KEY", "RUSTFS_SECRET_KEY", "RUSTFS_REGION", "RUSTFS_BUCKET"} {
				t.Setenv(key, "")
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			cfg, on, err := storageConfigFromEnv()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got cfg=%+v enabled=%v", cfg, on)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if on != tt.wantOn {
				t.Fatalf("enabled = %v, want %v", on, tt.wantOn)
			}
			if !on {
				return
			}
			if cfg != tt.want {
				t.Fatalf("config = %+v, want %+v", cfg, tt.want)
			}
		})
	}
}
