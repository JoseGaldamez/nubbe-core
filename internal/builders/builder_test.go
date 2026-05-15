package builders

import (
	"testing"
)

func TestGetBuilderByType(t *testing.T) {
	tests := []struct {
		projectType string
		wantErr     bool
	}{
		// Frontend (Cloudflare Pages)
		{"react", false},
		{"static", false},
		{"astro", false},
		{"vue", false},
		{"angular", false},

		// Backend / SSR (Cloud Run)
		{"nodejs", false},
		{"nextjs", false},
		{"python", false},
		{"flask", false},
		{"streamlit", false},
		{"go", false},

		// Invalid
		{"unknown", true},
		{"", true},
		{"ruby", true},
		{"java", true},
	}

	for _, tt := range tests {
		t.Run(tt.projectType, func(t *testing.T) {
			got, err := GetBuilderByType(tt.projectType)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetBuilderByType() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got == nil {
				t.Errorf("GetBuilderByType() got nil, want non-nil builder")
			}
		})
	}
}
