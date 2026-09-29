package http

import (
	"testing"

	"emsim/internal/auth"
)

func TestParseUserImportCSV(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   []auth.ImportRow
		issues []importIssueJSON
	}{
		{
			name: "comma, any column order, BOM, blank line",
			in:   "\ufeffrole,login,full_name,service_code\ntrainee,ivanov,Иванов Иван,dds_district\n\ninstructor,petrov,Петров П.,\n",
			want: []auth.ImportRow{
				{Row: 1, Login: "ivanov", FullName: "Иванов Иван", Role: auth.RoleTrainee, ServiceCode: strPtr("dds_district")},
				{Row: 2, Login: "petrov", FullName: "Петров П.", Role: auth.RoleInstructor},
			},
		},
		{
			name: "semicolon separator from a Russian spreadsheet",
			in:   "login;full_name;role\r\nsidorov;Сидоров С.;trainee\r\n",
			want: []auth.ImportRow{{Row: 1, Login: "sidorov", FullName: "Сидоров С.", Role: auth.RoleTrainee}},
		},
		{name: "missing required column", in: "login,full_name\na,b\n", issues: []importIssueJSON{{Row: 0, Field: "role", Reason: "missing_column"}}},
		{name: "empty file", in: "  \n", issues: []importIssueJSON{{Row: 0, Field: "file", Reason: "empty"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, issues := parseUserImportCSV([]byte(c.in))
			if len(issues) != len(c.issues) || (len(issues) > 0 && issues[0] != c.issues[0]) {
				t.Fatalf("issues = %+v, want %+v", issues, c.issues)
			}
			if len(rows) != len(c.want) {
				t.Fatalf("rows = %+v, want %+v", rows, c.want)
			}
			for i := range rows {
				got, want := rows[i], c.want[i]
				gs, ws := "", ""
				if got.ServiceCode != nil {
					gs = *got.ServiceCode
				}
				if want.ServiceCode != nil {
					ws = *want.ServiceCode
				}
				if got.Row != want.Row || got.Login != want.Login || got.FullName != want.FullName || got.Role != want.Role || gs != ws {
					t.Fatalf("row %d = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func strPtr(s string) *string { return &s }
