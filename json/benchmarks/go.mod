module github.com/go-openapi/core/json/benchmarks

// benchmarks require go1.26 (go-json-experiment)
go 1.26

require (
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68
	github.com/go-openapi/core/json v0.0.0-00010101000000-000000000000
	github.com/go-openapi/swag/pools v0.28.0
	github.com/go-openapi/testify/v2 v2.6.0
	github.com/mailru/easyjson v0.9.2
	github.com/pkg/profile v1.7.0
)

require (
	github.com/felixge/fgprof v0.9.5 // indirect
	github.com/go-openapi/swag/conv v0.28.0 // indirect
	github.com/google/pprof v0.0.0-20240227163752-401108e1b7e7 // indirect
	github.com/josharian/intern v1.0.0 // indirect
)

replace github.com/go-openapi/core/json => ../
