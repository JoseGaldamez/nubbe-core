package handlers

import (
	"testing"
)

func TestValidateGitHubSignature(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"ref":"refs/heads/main"}`)
	
	// Valid signature for the payload above and the secret
	validSignature := "sha256=b207d041ea2c868d2f0a04f9476df323457b51444dc832111123f3f753cffda5"

	tests := []struct {
		name      string
		payload   []byte
		signature string
		secret    string
		want      bool
	}{
		{
			name:      "valid signature",
			payload:   payload,
			signature: validSignature,
			secret:    secret,
			want:      true,
		},
		{
			name:      "invalid signature",
			payload:   payload,
			signature: "sha256=invalid",
			secret:    secret,
			want:      false,
		},
		{
			name:      "invalid prefix",
			payload:   payload,
			signature: "sha1=b207d041ea2c868d2f0a04f9476df323457b51444dc832111123f3f753cffda5",
			secret:    secret,
			want:      false,
		},
		{
			name:      "wrong secret",
			payload:   payload,
			signature: validSignature,
			secret:    "wrong-secret",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateGitHubSignature(tt.payload, tt.signature, tt.secret); got != tt.want {
				t.Errorf("validateGitHubSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuditDependencies(t *testing.T) {
	tests := []struct {
		name    string
		payload GitHubPushPayload
		want    bool
	}{
		{
			name: "modified package.json",
			payload: GitHubPushPayload{
				Commits: []struct {
					Modified []string `json:"modified"`
					Added    []string `json:"added"`
					Removed  []string `json:"removed"`
				}{
					{Modified: []string{"src/main.js", "package.json"}},
				},
			},
			want: true,
		},
		{
			name: "added go.mod",
			payload: GitHubPushPayload{
				Commits: []struct {
					Modified []string `json:"modified"`
					Added    []string `json:"added"`
					Removed  []string `json:"removed"`
				}{
					{Added: []string{"go.mod"}},
				},
			},
			want: true,
		},
		{
			name: "no dependency files",
			payload: GitHubPushPayload{
				Commits: []struct {
					Modified []string `json:"modified"`
					Added    []string `json:"added"`
					Removed  []string `json:"removed"`
				}{
					{Modified: []string{"README.md", "src/app.css"}},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auditDependencies(tt.payload); got != tt.want {
				t.Errorf("auditDependencies() = %v, want %v", got, tt.want)
			}
		})
	}
}
