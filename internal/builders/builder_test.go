package builders

import (
	"testing"
)

func TestGetBuilderByType(t *testing.T) {
	tests := []struct {
		projectType string
		wantErr     bool
	}{
		{"react", false},
		{"static", false},
		{"astro", false},
		{"nodejs", false},
		{"unknown", true},
		{"", true},
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
