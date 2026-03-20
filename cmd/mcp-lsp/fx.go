package main

import "go.uber.org/fx"

func run() error {
	app := fx.New(
		fx.NopLogger,
	)
	if app.Err() != nil {
		return app.Err()
	}
	return nil
}
