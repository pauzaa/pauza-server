package syncmodel_test

import (
	"encoding/json"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

func TestRequestValidateAndConvert_MinimalModes(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"tables": {
			"modes": {
				"cursor": 0,
				"upserts": [{
					"id": "mode-1",
					"title": "Focus",
					"text_on_screen": "Stay focused",
					"allowed_pauses_count": 1,
					"ending_pausing_scenario": "manual",
					"icon_token": "leaf",
					"created_at": 1,
					"updated_at": 2
				}],
				"deletions": []
			}
		}
	}`)

	var req syncmodel.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	tables, fields := req.ValidateAndConvert()
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if tables.Modes == nil {
		t.Fatal("expected modes table")
	}
	if len(tables.Modes.Upserts) != 1 {
		t.Fatalf("modes upserts = %d, want 1", len(tables.Modes.Upserts))
	}
	if tables.Modes.Upserts[0].EndingPausingScenario != syncmodel.ModeEndingPausingScenarioManual {
		t.Fatalf("ending_pausing_scenario = %q", tables.Modes.Upserts[0].EndingPausingScenario)
	}
}

func TestRequestValidateAndConvert_MissingCursor(t *testing.T) {
	t.Parallel()

	body := []byte(`{"tables":{"modes":{"upserts":[],"deletions":[]}}}`)

	var req syncmodel.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	_, fields := req.ValidateAndConvert()
	if got := fields["tables.modes.cursor"]; got != "cursor is required" {
		t.Fatalf("tables.modes.cursor = %q", got)
	}
}

func TestRequestRejectsInvalidEnumJSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"tables": {
			"mode_blocked_apps": {
				"cursor": 0,
				"upserts": [{
					"mode_id": "mode-1",
					"platform": "desktop",
					"app_identifier": "com.test.app",
					"created_at": 1,
					"updated_at": 2
				}],
				"deletions": []
			}
		}
	}`)

	var req syncmodel.Request
	if err := json.Unmarshal(body, &req); err == nil {
		t.Fatal("expected invalid enum json to fail")
	}
}
