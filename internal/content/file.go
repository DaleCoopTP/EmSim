package content

import (
	"encoding/json"
	"fmt"
	"io"
)

// File is scenario-file.schema.json's ScenarioFile — the on-disk wrapper
// a prepared scenario file (seed/scenarios/*.json) carries around one
// immutable Body: the stable Key an import keys scenario_versions rows
// by (independent of the file's name/path), an explicit Version number,
// and Title/Origin (scenarios.title/origin — content's Importer, added in
// C3, rejects a re-import that tries to change either for an existing
// key; see scenario-file.schema.json's doc comments).
type File struct {
	Schema  string `json:"schema"`
	Key     string `json:"key"`
	Version int    `json:"version"`
	Title   string `json:"title"`
	Origin  string `json:"origin"`
	Body    Body   `json:"body"`
}

// DecodeFile parses a scenario file: r's bytes must decode as JSON with no
// duplicate object keys anywhere (ErrDuplicateKey — encoding/json's
// ordinary Unmarshal silently keeps the last occurrence of a repeated
// key, which would let a corrupted or hand-edited file mean something
// different than what a reviewer read). It returns two views of the same
// document: raw, the json.Number-preserving generic form
// internal/content/schema.Validator and Digest expect, and file, the
// typed File decoded from it. DecodeFile does not itself check raw
// against scenario-file.schema.json or scenario.schema.json — call
// Validator.ValidateFile(raw) first; a document that fails schema
// validation may not decode cleanly into File at all.
func DecodeFile(r io.Reader) (raw any, file File, err error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	raw, err = decodeUnique(dec)
	if err != nil {
		return nil, File{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, File{}, fmt.Errorf("trailing content after JSON document")
	}

	// Re-marshal the duplicate-checked, number-preserving raw value and
	// decode it into the typed struct — simpler and just as safe as a
	// hand-written any-to-struct mapper, since raw's shape already
	// matches what encoding/json produces for its own decode.
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, File{}, fmt.Errorf("re-encode scenario file: %w", err)
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, File{}, fmt.Errorf("decode scenario file: %w", err)
	}
	return raw, file, nil
}

// decodeUnique reads one JSON value from dec, rejecting any object with a
// repeated key at any depth. Numbers decode as json.Number (dec must have
// UseNumber set) so Digest sees the literal the file actually contains.
func decodeUnique(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeValue(dec, tok)
}

func decodeValue(dec *json.Decoder, tok json.Token) (any, error) {
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil // string, json.Number, bool, or nil
	}

	switch delim {
	case '{':
		obj := make(map[string]any)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := keyTok.(string)
			if _, exists := obj[key]; exists {
				return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, key)
			}
			valTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			val, err := decodeValue(dec, valTok)
			if err != nil {
				return nil, err
			}
			obj[key] = val
		}
		if _, err := dec.Token(); err != nil { // consume closing '}'
			return nil, err
		}
		return obj, nil
	case '[':
		arr := make([]any, 0)
		for dec.More() {
			valTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			val, err := decodeValue(dec, valTok)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		if _, err := dec.Token(); err != nil { // consume closing ']'
			return nil, err
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

// bodyRaw extracts the "body" sub-value from a scenario file's raw form
// (DecodeFile's first return value) for Digest — content.Digest(bodyRaw)
// is scenario_versions.digest, sha256 of the body alone, not the file
// wrapper (scenario-file.schema.json's key/version/title/origin are
// import metadata, not part of the immutable version content).
func bodyRaw(raw any) (any, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scenario file: not a JSON object")
	}
	body, ok := obj["body"]
	if !ok {
		return nil, fmt.Errorf("scenario file: missing %q", "body")
	}
	return body, nil
}

// BodyDigest is a convenience over Digest(bodyRaw(raw)) for the common
// case: raw is DecodeFile's first return value, already schema-validated.
func BodyDigest(raw any) ([32]byte, error) {
	b, err := bodyRaw(raw)
	if err != nil {
		return [32]byte{}, err
	}
	return Digest(b), nil
}
