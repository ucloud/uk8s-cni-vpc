package snapshot

import (
	"errors"
	"testing"
)

func TestTakeSnapshot(t *testing.T) {
	s := New("test-snapshot")

	s.SetError(errors.New("test error"))
	s.SetDesc("This is a test snapshot")

	s.Add("ip", "addr")
	s.Add("ip", "link")
	s.Add("ip", "route")

	s.Run()
}
