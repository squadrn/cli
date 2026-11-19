package config

import (
	"errors"
	"testing"
)

func TestCheckPortable(t *testing.T) {
	for name, ok := range map[string]bool{
		"squadrn": true, "sqd": true, "ai": true, "con": false, "nul": false, "com1": false, "lpt9": false,
		"com0": true, "console": true, "gc": false, "ls": false, "iwr": false, "sc": false, "sx": true,
	} {
		err := CheckPortable(name)
		if (err == nil) != ok || (err != nil && !errors.Is(err, ErrInvalidCommand)) {
			t.Errorf("CheckPortable(%s) = %v", name, err)
		}
	}
}
