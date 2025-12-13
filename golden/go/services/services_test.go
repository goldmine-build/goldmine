package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.goldmine.build/go/deepequal/assertdeep"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		flags   []string
		want    []Service
		wantErr bool
	}{
		{
			name:    "empty is valid",
			flags:   []string{},
			want:    AllServices,
			wantErr: false,
		},
		{
			name:    "nil is valid",
			flags:   nil,
			want:    AllServices,
			wantErr: false,
		},
		{
			name:    "invalid services are caught",
			flags:   []string{"this is not a valid service"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "valid services are passed through",
			flags:   []string{"frontend", "ingester"},
			want:    []Service{Frontend, Ingester},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := Validate(tt.flags)
			if tt.wantErr {
				assert.Error(t, gotErr)
			} else {
				assert.NoError(t, gotErr)
			}
			assertdeep.Equal(t, got, tt.want)
		})
	}
}
