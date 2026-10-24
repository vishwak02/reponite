// Package orders bills and stores customer orders.
package orders

import (
	"errors"
	"log"
	"os"
)

// ErrDeclined is returned when the card is refused.
var ErrDeclined = errors.New("card declined")

// Order is one customer order.
type Order struct {
	ID    string
	Total int
}

// Charge bills an order's card.
func Charge(o Order) error {
	log.Printf("charging order %s", o.ID)
	if err := validateCard(o); err != nil {
		return err
	}
	return save(o)
}

func validateCard(o Order) error {
	if o.Total <= 0 {
		return ErrDeclined
	}
	return nil
}

func save(o Order) error {
	log.Printf("saved order %s", o.ID)
	return nil
}

// Port is where the server listens.
func Port() string {
	if p := os.Getenv("SHOP_PORT"); p != "" {
		return p
	}
	return "8080"
}
