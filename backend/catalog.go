package main

// Product is a catalog item. Price is in minor units (cents for EUR).
type Product struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"` // minor units
	Currency    string `json:"currency"`
}

var catalog = []Product{
	{ID: "tee", Name: "Moolah Tee", Description: "Heavyweight cotton tee", Price: 2500, Currency: "eur"},
	{ID: "mug", Name: "Moolah Mug", Description: "Ceramic mug, 350 ml", Price: 1500, Currency: "eur"},
	{ID: "stickers", Name: "Sticker pack", Description: "5 vinyl stickers", Price: 500, Currency: "eur"},
	{ID: "hoodie", Name: "Moolah Hoodie", Description: "Brushed fleece hoodie", Price: 6500, Currency: "eur"},
}

func findProduct(id string) (Product, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Product{}, false
}
