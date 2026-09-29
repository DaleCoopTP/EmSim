package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicNeverContainsSecrets(t *testing.T) {
	const password, apiKey = "s3cr3t-db-password", "sk-live-secret-key"
	apiValues := map[string]string{
		"DATABASE_URL":    "postgres://emsim:" + password + "@db.internal:5432/emsim?sslmode=disable",
		"API_LISTEN_ADDR": "127.0.0.1:8080", "ADMIN_LISTEN_ADDR": "127.0.0.1:8081",
		"DICTATION": "whisper", "STT_URL": "http://user:" + password + "@stt:8080/base?token=" + apiKey,
	}
	workerValues := map[string]string{
		"DATABASE_URL": apiValues["DATABASE_URL"], "WORKER_ID": "w", "WORKER_POLL_INTERVAL": "250ms", "WORKER_DRAIN_TIMEOUT": "10s",
		"WORKER_ADMIN_LISTEN_ADDR": "127.0.0.1:8082", "SHORT_CONCURRENCY": "1", "LLM_CONCURRENCY": "1", "STT_CONCURRENCY": "1",
		"REPORT_CONCURRENCY": "1", "CALLER_CONCURRENCY": "1", "CALLER_REPLY_TIMEOUT": "10s",
		"CALLER_REPLIER": "llm", "CALLER_LLM_URL": "https://bot:" + password + "@llm.example/v1?key=" + apiKey, "CALLER_LLM_MODEL": "m",
		"ASSESSMENT_JUDGE": "llm", "JUDGE_LLM_URL": "https://llm.example/v1", "JUDGE_LLM_MODEL": "m",
		"LLM_API_KEY": apiKey, "BACKUP_DIR": "/backups",
	}
	api, err := APIFromEnvironment(func(n string) string { return apiValues[n] })
	if err != nil {
		t.Fatal(err)
	}
	worker, err := WorkerFromEnvironment(func(n string) string { return workerValues[n] }, "all")
	if err != nil {
		t.Fatal(err)
	}
	for name, params := range map[string][]Param{"api": api.Public("/blobs"), "worker": worker.Public()} {
		raw, _ := json.Marshal(params)
		for _, secret := range []string{password, apiKey, "emsim:"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%s params leak %q: %s", name, secret, raw)
			}
		}
		if len(params) < 10 {
			t.Fatalf("%s params = %d entries", name, len(params))
		}
	}
	values := map[string]string{}
	for _, p := range worker.Public() {
		values[p.Env] = p.Value
	}
	if values["DATABASE_URL"] != "db.internal:5432/emsim" || values["LLM_API_KEY"] != "задан" || values["BACKUP_AT"] != "03:00" || values["CALLER_LLM_URL"] != "https://llm.example/v1" {
		t.Fatalf("worker values = %+v", values)
	}
}

func TestSafeURLAndTarget(t *testing.T) {
	if got := SafeURL("not a url"); got != "" {
		t.Fatalf("SafeURL(garbage) = %q", got)
	}
	if got := SafeDatabaseTarget("postgres://u:p@h:1/d"); got != "h:1/d" {
		t.Fatalf("SafeDatabaseTarget = %q", got)
	}
}
