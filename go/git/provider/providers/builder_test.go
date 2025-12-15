package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ownerRepoFromURL(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		url     string
		owner   string
		repo    string
		wantErr bool
	}{
		{
			name:    "Happy path",
			url:     "https://github.com/goldmine-build/goldmine.git",
			owner:   "goldmine-build",
			repo:    "goldmine",
			wantErr: false,
		},
		{
			name:    "Happy path",
			url:     "https://github.com/goldmine-build",
			owner:   "",
			repo:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got2, gotErr := ownerRepoFromURL(tt.url)
			if tt.wantErr {
				assert.Error(t, gotErr)
				return
			} else {
				assert.NoError(t, gotErr)
			}
			assert.Equal(t, tt.owner, got)
			assert.Equal(t, tt.repo, got2)
		})
	}
}
