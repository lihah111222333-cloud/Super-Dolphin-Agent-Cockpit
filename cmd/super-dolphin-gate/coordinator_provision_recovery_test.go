package main

import "testing"

func TestProductionConfigRejectsAuthorityIdentityAndPublicKeyReuse(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*productionCoordinatorConfig)
	}{
		{name: "receipt signer matches promotion", mutate: func(config *productionCoordinatorConfig) {
			config.ResultReceiptAuthority.Signer = config.PromotionSigner.Signer
		}},
		{name: "grant signer matches promotion", mutate: func(config *productionCoordinatorConfig) {
			config.ActionGrantAuthority.Signer = config.PromotionSigner.Signer
		}},
		{name: "grant signer matches receipt", mutate: func(config *productionCoordinatorConfig) {
			config.ActionGrantAuthority.Signer = config.ResultReceiptAuthority.Signer
		}},
		{name: "receipt public key matches promotion", mutate: func(config *productionCoordinatorConfig) {
			config.ResultReceiptAuthority.PublicKey = config.AcceptedImageSigners[0].PublicKey
		}},
		{name: "grant public key matches promotion", mutate: func(config *productionCoordinatorConfig) {
			config.ActionGrantAuthority.PublicKey = config.AcceptedImageSigners[0].PublicKey
		}},
		{name: "grant public key matches receipt", mutate: func(config *productionCoordinatorConfig) {
			config.ActionGrantAuthority.PublicKey = config.ResultReceiptAuthority.PublicKey
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductionTestFixture(t)
			config := fixture.config
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("production config accepted authority identity or public key reuse")
			}
		})
	}
}
