package auth

import "testing"

func TestAllowedMatchesRFC001Table(t *testing.T) {
	tests := []struct {
		role  Role
		group Group
		want  bool
	}{
		// auth: everyone.
		{RoleAdmin, GroupAuth, true},
		{RoleInstructor, GroupAuth, true},
		{RoleTrainee, GroupAuth, true},

		// admin: admin only.
		{RoleAdmin, GroupAdmin, true},
		{RoleInstructor, GroupAdmin, false},
		{RoleTrainee, GroupAdmin, false},

		// content/lessons/assessment/reports: instructor only — this is
		// the DoD checkpoint that admin never reaches lesson or
		// assessment content (slice-planning.md §2).
		{RoleInstructor, GroupContent, true},
		{RoleAdmin, GroupContent, false},
		{RoleTrainee, GroupContent, false},
		{RoleInstructor, GroupLessons, true},
		{RoleAdmin, GroupLessons, false},
		{RoleTrainee, GroupLessons, false},
		{RoleInstructor, GroupAssessment, true},
		{RoleAdmin, GroupAssessment, false},
		{RoleTrainee, GroupAssessment, false},
		{RoleInstructor, GroupReports, true},
		{RoleAdmin, GroupReports, false},
		{RoleTrainee, GroupReports, false},

		// trainee / trainee_self: trainee only.
		{RoleTrainee, GroupTrainee, true},
		{RoleAdmin, GroupTrainee, false},
		{RoleInstructor, GroupTrainee, false},
		{RoleTrainee, GroupTraineeSelf, true},
		{RoleInstructor, GroupTraineeSelf, false},
		{RoleAdmin, GroupTraineeSelf, false},

		// tasks: admin and instructor (whoever started the task), never
		// trainee.
		{RoleAdmin, GroupTasks, true},
		{RoleInstructor, GroupTasks, true},
		{RoleTrainee, GroupTasks, false},
	}
	for _, test := range tests {
		if got := Allowed(test.role, test.group); got != test.want {
			t.Errorf("Allowed(%s, %s) = %t, want %t", test.role, test.group, got, test.want)
		}
	}
}

func TestAllowedRejectsUnknownGroup(t *testing.T) {
	if Allowed(RoleAdmin, Group("nonexistent")) {
		t.Fatal("Allowed() = true for a group with no entry in the table")
	}
}

func TestAdminHasNoAccessToLessonOrAssessmentContent(t *testing.T) {
	// The explicit slice 1 DoD checkpoint (slice-planning.md §2:
	// "администратор не получает доступ к содержимому занятий и
	// оценкам") — spelled out as its own test so it survives an
	// accidental groupRoles edit even if the table-driven cases above
	// change shape.
	for _, group := range []Group{GroupContent, GroupLessons, GroupAssessment, GroupTrainee, GroupTraineeSelf, GroupReports} {
		if Allowed(RoleAdmin, group) {
			t.Errorf("Allowed(admin, %s) = true, want false", group)
		}
	}
}

func TestTraineeAndInstructorHaveNoAccessToAdminGroup(t *testing.T) {
	for _, role := range []Role{RoleInstructor, RoleTrainee} {
		if Allowed(role, GroupAdmin) {
			t.Errorf("Allowed(%s, admin) = true, want false", role)
		}
	}
}
