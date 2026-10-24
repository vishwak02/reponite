// Command server answers the shop's HTTP API.
package main

import (
	"encoding/json"
	"log"
	"net/http"

	"example.com/shop/orders"
)

func main() {
	http.HandleFunc("/api/orders", handleOrders)
	log.Fatal(http.ListenAndServe(":"+orders.Port(), nil))
}

func handleOrders(w http.ResponseWriter, r *http.Request) {
	var o orders.Order
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		http.Error(w, "bad order", http.StatusBadRequest)
		return
	}
	if err := orders.Charge(o); err != nil {
		http.Error(w, err.Error(), http.StatusPaymentRequired)
		return
	}
	w.WriteHeader(http.StatusCreated)
}
