module github.com/go-openapi/core/json/benchmarks

// go-json-experiment is forked and adapted by fredbi to support go1.25
go 1.25.0

require (
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68
	github.com/go-openapi/core/json v0.0.0-00010101000000-000000000000
	github.com/go-openapi/swag/pools v0.29.0
	github.com/go-openapi/testify/v2 v2.6.1
	github.com/mailru/easyjson v0.9.2
	github.com/pkg/profile v1.7.0
)

require (
	github.com/felixge/fgprof v0.9.5 // indirect
	github.com/go-openapi/swag/conv v0.29.0 // indirect
	github.com/google/pprof v0.0.0-20240227163752-401108e1b7e7 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
)

replace (
	github.com/go-json-experiment/json => github.com/fredbi/go-json-experiment v0.1.0
	github.com/go-openapi/core/json => ../
)
