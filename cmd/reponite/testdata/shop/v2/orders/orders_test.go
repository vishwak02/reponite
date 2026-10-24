package orders

import "testing"

func TestCharge(t *testing.T) {
	if err := Charge(Order{ID: "a1", Total: 5}); err != nil {
		t.Fatal(err)
	}
}

func TestChargeDeclined(t *testing.T) {
	if err := Charge(Order{ID: "a2"}); err != ErrDeclined {
		t.Fatalf("got %v", err)
	}
}
