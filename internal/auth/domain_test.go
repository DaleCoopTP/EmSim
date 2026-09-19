package auth

import (
	"errors"
	"strings"
	"testing"
)

func serviceCode(v string) *string { return &v }

func TestValidateLogin(t *testing.T) {
	tests := []struct {
		login string
		valid bool
	}{
		{"dispatcher-1", true},
		{"a.b_c-9", true},
		{"abc", true},
		{"ab", false},                    // too short
		{strings.Repeat("a", 65), false}, // too long
		{"", false},
		{"Dispatcher", false}, // uppercase not allowed
		{"dispatcher 1", false},
		{"диспетчер", false},
	}
	for _, test := range tests {
		err := ValidateLogin(test.login)
		if test.valid && err != nil {
			t.Errorf("ValidateLogin(%q) error = %v, want nil", test.login, err)
		}
		if !test.valid && err == nil {
			t.Errorf("ValidateLogin(%q) error = nil, want an error", test.login)
		}
		if err != nil && !errors.Is(err, ErrValidation) {
			t.Errorf("ValidateLogin(%q) error = %v, want it to match ErrValidation", test.login, err)
		}
	}
}

func TestValidateFullName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"Иванов Иван Иванович", true},
		{"", false},
		{"   ", false},
		{strings.Repeat("и", 201), false},
		{strings.Repeat("и", 200), true},
	}
	for _, test := range tests {
		err := ValidateFullName(test.name)
		if test.valid && err != nil {
			t.Errorf("ValidateFullName(%q) error = %v, want nil", test.name, err)
		}
		if !test.valid && err == nil {
			t.Errorf("ValidateFullName(%q) error = nil, want an error", test.name)
		}
	}
}

func baseNewUser(role Role) NewUser {
	return NewUser{
		Login:    "dispatcher-1",
		Password: "correct-horse",
		FullName: "Иванов Иван Иванович",
		Role:     role,
	}
}

func TestValidateNewUserTraineeRequiresServiceCode(t *testing.T) {
	n := baseNewUser(RoleTrainee)
	if err := ValidateNewUser(n); err == nil {
		t.Fatal("trainee without service_code was accepted")
	}
	var ve *ValidationError
	n.ServiceCode = serviceCode("dds_district")
	if err := ValidateNewUser(n); err != nil {
		t.Fatalf("ValidateNewUser() error = %v, want nil", err)
	}
	n.ServiceCode = serviceCode("   ")
	if err := ValidateNewUser(n); !errors.As(err, &ve) || ve.Field != "service_code" {
		t.Fatalf("blank service_code accepted or wrong field: err=%v", err)
	}
}

func TestValidateNewUserAdminInstructorRejectServiceCode(t *testing.T) {
	for _, role := range []Role{RoleAdmin, RoleInstructor} {
		n := baseNewUser(role)
		n.ServiceCode = serviceCode("dds_district")
		var ve *ValidationError
		err := ValidateNewUser(n)
		if !errors.As(err, &ve) || ve.Field != "service_code" || ve.Reason != "not_allowed" {
			t.Errorf("role=%s: ValidateNewUser() error = %v, want service_code/not_allowed", role, err)
		}
	}
}

func TestValidateNewUserAdminInstructorWithoutServiceCode(t *testing.T) {
	for _, role := range []Role{RoleAdmin, RoleInstructor} {
		n := baseNewUser(role)
		if err := ValidateNewUser(n); err != nil {
			t.Errorf("role=%s: ValidateNewUser() error = %v, want nil", role, err)
		}
	}
}

func TestValidateNewUserRejectsBadPasswordLength(t *testing.T) {
	n := baseNewUser(RoleAdmin)
	n.Password = "short"
	if err := ValidateNewUser(n); err == nil {
		t.Fatal("7-byte password was accepted")
	}
	n.Password = strings.Repeat("a", 257)
	if err := ValidateNewUser(n); err == nil {
		t.Fatal("257-byte password was accepted")
	}
	n.Password = strings.Repeat("a", 8)
	if err := ValidateNewUser(n); err != nil {
		t.Fatalf("8-byte password rejected: %v", err)
	}
}

func TestValidateNewUserRejectsInvalidRole(t *testing.T) {
	if err := ValidateNewUser(baseNewUser(Role("superadmin"))); err == nil {
		t.Fatal("unknown role was accepted")
	}
}

func TestValidateUserPatchIgnoresUntouchedServiceCode(t *testing.T) {
	current := User{Role: RoleTrainee, ServiceCode: serviceCode("dds_district")}
	patch := Patch{FullName: serviceCode("New Name")}
	if err := ValidateUserPatch(current, patch); err != nil {
		t.Fatalf("ValidateUserPatch() error = %v, want nil", err)
	}
}

func TestValidateUserPatchRoleChangeRevalidatesServiceCode(t *testing.T) {
	current := User{Role: RoleTrainee, ServiceCode: serviceCode("dds_district")}
	newRole := RoleInstructor
	patch := Patch{Role: &newRole}
	if err := ValidateUserPatch(current, patch); err == nil {
		t.Fatal("promoting a trainee to instructor without clearing service_code was accepted")
	}

	clear := ""
	patch.ServiceCode = &clear
	if err := ValidateUserPatch(current, patch); err != nil {
		t.Fatalf("ValidateUserPatch() error = %v, want nil once service_code is cleared", err)
	}
}

func TestValidateUserPatchClearingServiceCodeForTraineeIsRejected(t *testing.T) {
	current := User{Role: RoleTrainee, ServiceCode: serviceCode("dds_district")}
	clear := ""
	patch := Patch{ServiceCode: &clear}
	if err := ValidateUserPatch(current, patch); err == nil {
		t.Fatal("clearing a trainee's service_code without changing role was accepted")
	}
}

func TestValidateUserPatchSettingServiceCodeForInstructorIsRejected(t *testing.T) {
	current := User{Role: RoleInstructor, ServiceCode: nil}
	code := "dds_district"
	patch := Patch{ServiceCode: &code}
	if err := ValidateUserPatch(current, patch); err == nil {
		t.Fatal("setting an instructor's service_code was accepted")
	}
}

func TestValidateUserPatchRejectsBadPasswordAndFullName(t *testing.T) {
	current := User{Role: RoleAdmin}
	short := "short"
	if err := ValidateUserPatch(current, Patch{Password: &short}); err == nil {
		t.Fatal("short password patch was accepted")
	}
	blank := "   "
	if err := ValidateUserPatch(current, Patch{FullName: &blank}); err == nil {
		t.Fatal("blank full_name patch was accepted")
	}
}

func TestValidateUserPatchEmptyPatchIsValid(t *testing.T) {
	current := User{Role: RoleTrainee, ServiceCode: serviceCode("dds_district")}
	if err := ValidateUserPatch(current, Patch{}); err != nil {
		t.Fatalf("ValidateUserPatch(empty) error = %v, want nil", err)
	}
}

func TestValidationErrorUnwrapsToErrValidation(t *testing.T) {
	err := ValidateLogin("")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("errors.Is(err, ErrValidation) = false for %v", err)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "login" {
		t.Fatalf("errors.As() = %v, want a *ValidationError with Field=login", err)
	}
}
