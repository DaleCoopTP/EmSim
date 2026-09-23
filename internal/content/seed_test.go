package content_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"emsim/internal/content"
	"emsim/internal/content/schema"
)

// mapCatalog is a Catalog backed by the seed files themselves — this test
// has no database, so it checks the seed exactly the way
// content.Service.ImportScenarios would once services.json/classifier.json
// are already imported.
type mapCatalog struct {
	services   map[string]content.ServiceRecord
	classifier map[string]string
	versions   map[string]content.ScenarioVersionReference
}

func (c mapCatalog) Service(code string) (content.ServiceRecord, bool) {
	s, ok := c.services[code]
	return s, ok
}

func (c mapCatalog) ClassifierType(code string) (string, bool) {
	name, ok := c.classifier[code]
	return name, ok
}

func (c mapCatalog) ScenarioVersion(key string, version int) (content.ScenarioVersionReference, bool) {
	ref, ok := c.versions[key+"@"+strconv.Itoa(version)]
	return ref, ok
}

// TestSeedFilesAreValid loads the real seed/ files shipped for slice 2
// (seed/README.md) and checks they pass the same schema and semantic
// validation `emsim import` runs — a change to the seed data that would
// make the import command reject it fails here first.
func TestSeedFilesAreValid(t *testing.T) {
	root := repoRoot(t)
	seedDir := filepath.Join(root, "seed")

	services, err := content.DecodeServiceDefs(openFile(t, filepath.Join(seedDir, "services.json")))
	if err != nil {
		t.Fatalf("decode services.json: %v", err)
	}
	if len(services) == 0 {
		t.Fatalf("services.json has no entries")
	}
	catalog := mapCatalog{services: map[string]content.ServiceRecord{}, classifier: map[string]string{}, versions: map[string]content.ScenarioVersionReference{}}
	for _, s := range services {
		catalog.services[s.Code] = s
	}

	classifierTypes, err := content.DecodeClassifierDefs(openFile(t, filepath.Join(seedDir, "classifier.json")))
	if err != nil {
		t.Fatalf("decode classifier.json: %v", err)
	}
	if len(classifierTypes) == 0 {
		t.Fatalf("classifier.json has no entries")
	}
	for _, c := range classifierTypes {
		catalog.classifier[c.Code] = c.Name
	}

	validator, err := schema.New()
	if err != nil {
		t.Fatalf("schema.New: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(seedDir, "scenarios"))
	if err != nil {
		t.Fatalf("read seed/scenarios: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("seed/scenarios has no files")
	}

	seenKeys := map[string]map[int]bool{}
	files := make(map[string]content.File, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(seedDir, "scenarios", entry.Name())
		raw, file, err := content.DecodeFile(openFile(t, path))
		if err != nil {
			t.Fatalf("%s: DecodeFile: %v", entry.Name(), err)
		}
		if err := validator.ValidateFile(raw); err != nil {
			t.Fatalf("%s: schema validation: %v", entry.Name(), err)
		}
		if seenKeys[file.Key] == nil {
			seenKeys[file.Key] = map[int]bool{}
		}
		if seenKeys[file.Key][file.Version] {
			t.Fatalf("%s: duplicate scenario version %q@%d in seed/scenarios", entry.Name(), file.Key, file.Version)
		}
		seenKeys[file.Key][file.Version] = true
		files[entry.Name()] = file
		catalog.versions[file.Key+"@"+strconv.Itoa(file.Version)] = content.ScenarioVersionReference{
			Status: "approved", Published: true, ExerciseType: file.Body.ExerciseType, TargetService: file.Body.TargetService,
		}
	}
	for key, versions := range seenKeys {
		for version := 1; version <= len(versions); version++ {
			if !versions[version] {
				t.Fatalf("scenario %s has a version gap at %d", key, version)
			}
		}
	}
	for name, file := range files {
		if err := content.Validate(file.Body, catalog); err != nil {
			t.Fatalf("%s: semantic validation: %v", name, err)
		}
	}
	if len(seenKeys) < 2 {
		t.Fatalf("seed/scenarios has %d distinct keys, want at least the 2 pilot cases", len(seenKeys))
	}
}

func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}
