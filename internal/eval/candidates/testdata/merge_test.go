package p

import "testing"

func TestUnchanged(t *testing.T) { t.Log("same body") }

func TestChanged(t *testing.T) {
	if Answer() != 2 {
		t.Fatalf("Answer() = %d, want 2", Answer())
	}
}

func TestAdded(t *testing.T) {}

func helper() { println("new helper") }
