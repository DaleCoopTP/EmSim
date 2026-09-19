package auth

// Group names a set of API routes sharing the same role requirement
// (RFC-001 §5's route table, openapi.yaml tags). internal/auth/http's
// RequireRole (added later in slice 1) looks a route's Group up here
// rather than hard-coding a role per handler, so the "who can call what"
// table lives in exactly one place.
type Group string

const (
	// GroupAuth is /auth/login, /auth/logout, /me — RFC-001 §5: "все".
	GroupAuth Group = "auth"
	// GroupAdmin is /admin/* — users, workstations, import, backup,
	// status, task retry.
	GroupAdmin Group = "admin"
	// GroupContent is /services, /scenarios/* — instructor-only per
	// RFC-001 §5; content.go's own read path for a trainee's assigned
	// scenario goes through GroupTrainee's item endpoints instead, not
	// through this group.
	GroupContent Group = "content"
	// GroupLessons is /lessons/* (create, assignments, start, stop,
	// monitor, stream) — instructor.
	GroupLessons Group = "lessons"
	// GroupTrainee is /my/*, /items/*, /items/*/calls/*/recording —
	// trainee.
	GroupTrainee Group = "trainee"
	// GroupAssessment is the instructor side of /items/*/assessment*,
	// /users/*/recommendation.
	GroupAssessment Group = "assessment"
	// GroupTraineeSelf is a trainee's own read access to their assessment
	// and progress (RFC-001 §5: "assessment | ... | instructor (+
	// trainee чтение своего)") — kept distinct from GroupAssessment
	// because it is read-only and scoped to the caller's own resource,
	// never another trainee's.
	GroupTraineeSelf Group = "trainee_self"
	// GroupReports is /lessons/*/report*, /groups/progress,
	// /users/*/progress — instructor.
	GroupReports Group = "reports"
	// GroupTasks is GET /tasks/{id} — polling status of a background task
	// (scenario generation, import, report build) the caller itself
	// started. openapi.yaml tags it "tasks", distinct from "admin", even
	// though /admin/tasks/{id}/retry shares the tag; RFC-001 §5 does not
	// give this group its own row. Until the tasks-polling endpoint is
	// actually built, admin and instructor are the two roles that ever
	// start a task, so both are allowed here — this is a placeholder to
	// revisit against the real handler, not a contract-derived rule.
	GroupTasks Group = "tasks"
)

// groupRoles is the one-place role table Allowed reads.
var groupRoles = map[Group][]Role{
	GroupAuth:        {RoleAdmin, RoleInstructor, RoleTrainee},
	GroupAdmin:       {RoleAdmin},
	GroupContent:     {RoleInstructor},
	GroupLessons:     {RoleInstructor},
	GroupTrainee:     {RoleTrainee},
	GroupAssessment:  {RoleInstructor},
	GroupTraineeSelf: {RoleTrainee},
	GroupReports:     {RoleInstructor},
	GroupTasks:       {RoleAdmin, RoleInstructor},
}

// Allowed reports whether role may call a route in group.
func Allowed(role Role, group Group) bool {
	for _, allowedRole := range groupRoles[group] {
		if allowedRole == role {
			return true
		}
	}
	return false
}
