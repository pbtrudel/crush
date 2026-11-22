package p

import "testing"

func TestUnchanged(t *testing.T) { t.Log("same body") }

func TestChanged(t *testing.T) {
	if Answer() != 1 {
		t.Fatalf("Answer() = %d, want 1", Answer())
	}
}

func TestRemoved(t *testing.T) {}

func helper() { println("old helper") }
