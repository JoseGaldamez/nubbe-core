package handlers

import (
	"testing"
)

func TestSanitizeSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple valid lowercase",
			input: "my-cool-project",
			want:  "my-cool-project",
		},
		{
			name:  "uppercase letters are lowercased",
			input: "My-Cool-Project",
			want:  "my-cool-project",
		},
		{
			name:  "special characters removed",
			input: "my_cool_project!123",
			want:  "mycoolproject123",
		},
		{
			name:  "leading and trailing hyphens removed",
			input: "-my-project-",
			want:  "my-project",
		},
		{
			name:  "multiple consecutive hyphens collapsed",
			input: "my---project--name",
			want:  "my-project-name",
		},
		{
			name:  "whitespaces trimmed and special characters removed",
			input: "  My Awesome Project!  ",
			want:  "myawesomeproject",
		},
		{
			name:  "dots and slashes removed",
			input: "subdomain.domain.com/path",
			want:  "subdomaindomaincompath",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSubdomain(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeSubdomain(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid regular length",
			input: "my-app",
			want:  true,
		},
		{
			name:  "valid boundary minimum",
			input: "abc",
			want:  true,
		},
		{
			name:  "invalid too short",
			input: "ab",
			want:  false,
		},
		{
			name:  "invalid empty",
			input: "",
			want:  false,
		},
		{
			name:  "invalid too long",
			input: "this-is-a-super-long-subdomain-that-exceeds-the-maximum-dns-label-length-of-sixty-three-characters",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidSubdomain(tt.input)
			if got != tt.want {
				t.Errorf("IsValidSubdomain(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsReservedSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "admin is reserved",
			input: "admin",
			want:  true,
		},
		{
			name:  "api is reserved",
			input: "api",
			want:  true,
		},
		{
			name:  "www is reserved",
			input: "www",
			want:  true,
		},
		{
			name:  "regular subdomain is not reserved",
			input: "my-awesome-startup",
			want:  false,
		},
		{
			name:  "similar but not exact match is not reserved",
			input: "admin-dashboard",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsReservedSubdomain(tt.input)
			if got != tt.want {
				t.Errorf("IsReservedSubdomain(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
