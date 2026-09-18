// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// migrations/embed.go; as-is.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
