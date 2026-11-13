package handlers

import "testing"

func TestGoogleEmailVerified(t *testing.T) {
	cases := []struct {
		claims map[string]interface{}
		want   bool
	}{
		{map[string]interface{}{"email_verified": true}, true},
		{map[string]interface{}{"email_verified": "true"}, true},
		{map[string]interface{}{"email_verified": false}, false},
		{map[string]interface{}{"email_verified": "false"}, false},
		{map[string]interface{}{"email_verified": 1}, false},
		{map[string]interface{}{}, false},
	}
	for _, tc := range cases {
		if got := googleEmailVerified(tc.claims); got != tc.want {
			t.Errorf("googleEmailVerified(%v) = %v, want %v", tc.claims, got, tc.want)
		}
	}
}
