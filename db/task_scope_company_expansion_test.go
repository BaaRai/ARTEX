package db

import (
	"fmt"
	"testing"
	"time"
)

// A domain the agent adds under a company that is already in the task's declared
// scope does not widen the scope: the company's own domain rules cover it.
func TestAddAgentScopeUnderDeclaredCompanyIsNotExpansion(t *testing.T) {
	d, assets, companies := testSetup(t)
	t.Cleanup(func() { d.Close() })

	stamp := time.Now().UnixNano()
	root := fmt.Sprintf("acme%d.com", stamp)
	companyID, _, _, _, _, err := companies.CreateCompanyWithScope(fmt.Sprintf("acme-%d", stamp), "",
		[]ScopeInput{{Value: root}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "company", companies.DeleteCompany(companyID)) })

	task, err := d.CreateTask("company scope", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupErr(t, "task", d.DeleteTask(task.ID)) })

	if _, err := assets.AddAgentScope(task.ID, "company", fmt.Sprint(companyID), "declared", "manual"); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ kind, value string }{
		{"subdomain", "api." + root},
		{"root_domain", root},
	} {
		ts, err := assets.AddAgentScope(task.ID, c.kind, c.value, "agent", "agent")
		if err != nil {
			t.Fatalf("%s %s: %v", c.kind, c.value, err)
		}
		if ts.Expanded {
			t.Errorf("%s %s: expanded=true, want false (covered by company %d scope %s)", c.kind, c.value, companyID, root)
		}
	}

	outside := fmt.Sprintf("other%d.com", stamp)
	ts, err := assets.AddAgentScope(task.ID, "root_domain", outside, "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if !ts.Expanded {
		t.Errorf("root_domain %s: expanded=false, want true (outside the company scope)", outside)
	}
}
