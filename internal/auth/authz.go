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
	// GroupContent is /scenarios/* — instructor-only per RFC-001 §5;
	// content.go's own read path for a trainee's assigned scenario goes
	// through GroupTrainee's item endpoints instead, not through this
	// group.
	GroupContent Group = "content"
	// GroupServices is GET /services — admin and instructor (openapi.yaml:
	// an admin needs the service catalogue to pick a trainee's
	// service_code in /admin/users, but gets no access to scenario
	// content/эталоны through it).
	GroupServices Group = "services"
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
	// GroupItemRead is GET /items/{itemId} (slice 3, RFC-001 §5's
	// "trainee | ... /items/{id}"): both an instructor viewing their own
	// lesson's item with its reference, and a trainee viewing their own
	// item without it. Neither GroupLessons nor GroupTrainee alone covers
	// both roles for this one route, and RequireRole checks only one
	// group per route (middleware.go), so this route needs its own group;
	// the handler itself still tells the two views apart and enforces
	// ownership.
	GroupItemRead Group = "item_read"
	// GroupItemActions is POST /items/{itemId}/actions (112-7/ADR-027): a
	// trainee acting on their own item, as before, plus an instructor
	// acting on their own operator-112 preview run's sole item (that run
	// has no workstation and no other participant). training.Service.
	// Execute still rejects any call whose run.UserID != actor.UserID
	// regardless of role, so this route grant alone never lets an
	// instructor touch another user's item — it only removes the
	// route-level block that would otherwise stop a preview author from
	// reaching their own item's command endpoint.
	GroupItemActions Group = "item_actions"
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
	GroupServices:    {RoleAdmin, RoleInstructor},
	GroupLessons:     {RoleInstructor},
	GroupTrainee:     {RoleTrainee},
	GroupAssessment:  {RoleInstructor},
	GroupTraineeSelf: {RoleTrainee},
	GroupItemRead:    {RoleInstructor, RoleTrainee},
	GroupItemActions: {RoleInstructor, RoleTrainee},
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
