// Package schema owns the single GraphQL schema used for execution and help.
package schema

import _ "embed"

//go:embed v1.graphqls
var SDL string
