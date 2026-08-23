module github.com/go-openapi/core/json/lexers/yaml-lexer

go 1.25.0

require (
	github.com/go-openapi/core/json v0.0.3
	github.com/go-openapi/swag/pools v0.29.0
	github.com/go-openapi/testify/v2 v2.6.1
	github.com/goccy/go-yaml v1.19.2
)

require github.com/go-openapi/swag/conv v0.29.0 // indirect

replace github.com/go-openapi/core/json => ../..
