// Package contracts embeds the executable JSON Schema contracts (as-is,
// same files design-docs/contracts/check.py validates offline) so the
// emsim binary carries them without a runtime file dependency — the
// pattern migrations/embed.go already uses for *.sql. internal/content's
// schema validator (internal/content/schema) is the first consumer;
// rubric.default.json is embedded too since internal/content.Validate
// checks scenario.reference.scoring against its criterion ids.
// rubric.operator112.v3.json (ADR-028) adds the LLM DESCRIPTION_CONTENT
// criterion on top of rubric-v2 — v2 stays embedded unchanged so a
// lesson frozen on it keeps scoring against it (ADR-013).
package contracts

import "embed"

//go:embed scenario.schema.json scenario-file.schema.json rubric.default.json rubric.operator112.json rubric.operator112.v1.json rubric.operator112.v3.json
var Files embed.FS
