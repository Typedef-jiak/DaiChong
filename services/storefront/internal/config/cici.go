package config

import "gptibo/storefront/internal/cici"

func LoadCici() (*cici.Service, error) {
	s := &cici.Service{}
	enabled, err := flag("CICI_ENABLED")
	if err != nil || !enabled {
		return s, err
	}
	key, err := Secret("CICI_API_KEY")
	if err != nil {
		return nil, err
	}
	secret, err := Secret("CICI_API_SECRET")
	if err != nil {
		return nil, err
	}
	s.Client, err = cici.NewClient(key, secret)
	if err != nil {
		return nil, err
	}
	s.PurchasesEnabled, err = flag("CICI_PURCHASES_ENABLED")
	return s, err
}
