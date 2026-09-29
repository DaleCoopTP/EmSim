package config

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Param is one effective setting as the administrator's read-only
// configuration screen shows it (ADR-038): the environment variable it
// comes from and its value in this process. Secrets are never a value —
// an API key appears only as "задан"/"не задан" style presence, and a
// URL loses its credentials. Group is a stable id the web client
// translates; Env is the variable to change in .env on the server.
type Param struct {
	Group string `json:"group"`
	Env   string `json:"env"`
	Value string `json:"value"`
}

// Group ids of Param.Group.
const (
	GroupDatabase    = "database"
	GroupSecurity    = "security"
	GroupPerformance = "performance"
	GroupModels      = "models"
	GroupBackup      = "backup"
	GroupLogging     = "logging"
	GroupProcess     = "process"
)

// SafeDatabaseTarget reduces a database URL to "host:port/name": the user
// name, password and every option are dropped.
func SafeDatabaseTarget(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host + parsed.Path
}

// SafeURL keeps scheme, host and path of a service URL and drops
// credentials, query and fragment.
func SafeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func presence(secret string) string {
	if secret == "" {
		return "не задан"
	}
	return "задан"
}

func yesNo(v bool) string { return strconv.FormatBool(v) }

func clock(offset time.Duration) string {
	minutes := int(offset / time.Minute)
	return twoDigits(minutes/60) + ":" + twoDigits(minutes%60)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Public lists the api's effective settings. blobRoot is BLOB_ROOT, which
// the api reads outside this struct.
func (c API) Public(blobRoot string) []Param {
	params := []Param{
		{GroupDatabase, "DATABASE_URL", SafeDatabaseTarget(c.DatabaseURL)},
		{GroupProcess, "API_LISTEN_ADDR", c.PublicAddr},
		{GroupProcess, "ADMIN_LISTEN_ADDR", c.AdminAddr},
		{GroupProcess, "BLOB_ROOT", blobRoot},
		{GroupSecurity, "SESSION_TTL", c.SessionTTL.String()},
		{GroupSecurity, "COOKIE_SECURE", yesNo(c.CookieSecure)},
		{GroupSecurity, "LOGIN_LOCKOUT_ATTEMPTS", strconv.Itoa(c.LoginLockoutAttempts)},
		{GroupSecurity, "LOGIN_LOCKOUT_DURATION", c.LoginLockoutDuration.String()},
		{GroupSecurity, "PASSWORD_MIN_LENGTH", strconv.Itoa(c.PasswordMinLength)},
		{GroupSecurity, "PASSWORD_FORCE_CHANGE", forceChangeLabel(c.PasswordForceChange)},
		{GroupLogging, "LOG_LEVEL", c.LogLevel},
		{GroupModels, "ASSESSMENT_JUDGE", c.AssessmentJudge},
		{GroupModels, "CALLER_WARMUP", yesNo(c.CallerWarmup)},
		{GroupModels, "CALLER_OPENING_DELAY", c.CallerOpeningDelay.String()},
		{GroupModels, "DICTATION", c.Dictation.Engine},
	}
	if c.Dictation.Engine != "" && c.Dictation.Engine != DictationOff {
		params = append(params,
			Param{GroupModels, "STT_URL", SafeURL(c.Dictation.STTURL)},
			Param{GroupModels, "STT_LANGUAGE", c.Dictation.Language},
			Param{GroupModels, "STT_MODEL", c.Dictation.Model},
			Param{GroupPerformance, "STT_TIMEOUT", c.Dictation.Timeout.String()},
			Param{GroupPerformance, "DICTATION_QUEUE_WAIT", c.Dictation.QueueWait.String()},
			Param{GroupPerformance, "DICTATION_CONCURRENCY", strconv.Itoa(c.Dictation.Concurrency)},
			Param{GroupPerformance, "DICTATION_MAX_SECONDS", strconv.Itoa(c.Dictation.MaxSeconds)},
		)
	}
	return params
}

// Public lists this worker's effective settings; the worker publishes
// them as a heartbeat so the api's configuration screen can show them.
func (c Worker) Public() []Param {
	zone := ""
	if c.ScheduleLocation != nil {
		zone = c.ScheduleLocation.String()
	}
	params := []Param{
		{GroupDatabase, "DATABASE_URL", SafeDatabaseTarget(c.DatabaseURL)},
		{GroupProcess, "WORKER_ID", c.WorkerID},
		{GroupProcess, "WORKER_POLL_INTERVAL", c.PollInterval.String()},
		{GroupProcess, "WORKER_DRAIN_TIMEOUT", c.DrainTimeout.String()},
		{GroupPerformance, "SHORT_CONCURRENCY", strconv.Itoa(c.ShortConcurrency)},
		{GroupPerformance, "LLM_CONCURRENCY", strconv.Itoa(c.LLMConcurrency)},
		{GroupPerformance, "STT_CONCURRENCY", strconv.Itoa(c.STTConcurrency)},
		{GroupPerformance, "REPORT_CONCURRENCY", strconv.Itoa(c.ReportConcurrency)},
		{GroupPerformance, "CALLER_CONCURRENCY", strconv.Itoa(c.CallerConcurrency)},
		{GroupPerformance, "CALLER_REPLY_TIMEOUT", c.CallerReplyTimeout.String()},
		{GroupPerformance, "JUDGE_TIMEOUT", c.JudgeTimeout.String()},
		{GroupPerformance, "JUDGE_MAX_TOKENS", strconv.Itoa(c.JudgeMaxTokens)},
		{GroupLogging, "LOG_LEVEL", c.LogLevel},
		{GroupLogging, "AUDIT_RETENTION_DAYS", strconv.Itoa(c.AuditRetentionDays)},
		{GroupLogging, "AUDIT_PRUNE_AT", clock(c.AuditPruneAt)},
		{GroupLogging, "SCHEDULE_TZ", zone},
		{GroupModels, "CALLER_REPLIER", c.CallerReplier},
		{GroupModels, "ASSESSMENT_JUDGE", c.AssessmentJudge},
		{GroupModels, "LLM_DIALECT", string(c.LLMDialect)},
		{GroupModels, "LLM_API_KEY", presence(firstNonEmpty(c.CallerLLMAPIKey, c.JudgeLLMAPIKey))},
		{GroupBackup, "BACKUP_DIR", c.BackupDir},
		{GroupBackup, "BACKUP_KEEP", strconv.Itoa(c.BackupKeep)},
		{GroupBackup, "BACKUP_AT", clock(c.BackupAt)},
	}
	if c.CallerReplier == CallerReplierLLM {
		params = append(params,
			Param{GroupModels, "CALLER_LLM_URL", SafeURL(c.CallerLLMURL)},
			Param{GroupModels, "CALLER_LLM_MODEL", c.CallerLLMModel},
			Param{GroupModels, "CALLER_TEMPERATURE", strconv.FormatFloat(c.CallerTemperature, 'f', -1, 64)},
			Param{GroupModels, "CALLER_TOP_P", strconv.FormatFloat(c.CallerTopP, 'f', -1, 64)},
			Param{GroupModels, "CALLER_REPEAT_PENALTY", strconv.FormatFloat(c.CallerRepeatPenalty, 'f', -1, 64)},
			Param{GroupModels, "CALLER_MAX_TOKENS", strconv.Itoa(c.CallerMaxTokens)},
		)
	}
	if c.AssessmentJudge == AssessmentJudgeLLM {
		params = append(params,
			Param{GroupModels, "JUDGE_LLM_URL", SafeURL(c.JudgeLLMURL)},
			Param{GroupModels, "JUDGE_LLM_MODEL", c.JudgeLLMModel},
		)
	}
	return params
}

func forceChangeLabel(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ",")
}
