package model

import "testing"

func TestTableNames(t *testing.T) {
	cases := map[string]string{
		Account{}.TableName():            "account",
		Vehicle{}.TableName():            "vehicle",
		Application{}.TableName():        "application",
		ApplicationVersion{}.TableName(): "application_version",
		Campaign{}.TableName():           "campaign",
		Release{}.TableName():            "vehicle_release",
		Receipt{}.TableName():            "receipt",
		ExceptionRecord{}.TableName():    "exception_record",
		AuditLog{}.TableName():           "audit_log",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("表名 %q, 期望 %q", got, want)
		}
	}
}
