package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// No read endpoint returns a stored secret in clear, and a masked placeholder
// sent back on write never replaces the stored secret.
func TestSecretsNeverReturnedInClear(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.PG()
	secrets := []string{"sk-llm-plain-0001", "mcp-env-plain-0002", "cred-plain-0003", "SECding-plain-0004", "tok-plain-0005"}
	task, err := s.m.CreateTask("secrets api", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	llmID, err := pg.SaveProfile(&db.LLMProfile{Name: "t-secapi", Format: "openai", Model: "m", APIKey: secrets[0]})
	mcpID, err2 := pg.SaveMCP(&db.MCPServer{Name: "t-secapi", Transport: "http", Env: json.RawMessage(`{"X-API-Key":"` + secrets[1] + `","EMPTY":""}`)})
	_, err3 := pg.UpsertTaskCredential(taskID, db.TaskCredentialInput{Domain: "x.example", Username: "u", Password: secrets[2]})
	chID, err4 := pg.SaveNotificationChannel(context.Background(), &db.NotificationChannel{Name: "t-secapi", Kind: "dingtalk",
		Config: json.RawMessage(`{"webhook":"https://oapi.example/send?access_token=` + secrets[4] + `","secret":"` + secrets[3] + `"}`)})
	t.Cleanup(func() {
		pg.Exec(`DELETE FROM llm_profiles WHERE id=$1`, llmID)
		pg.Exec(`DELETE FROM mcp_servers WHERE id=$1`, mcpID)
		pg.Exec(`DELETE FROM notification_channels WHERE id=$1`, chID)
		pg.DeleteTask(taskID)
	})
	for _, e := range []error{err, err2, err3, err4} {
		if e != nil {
			t.Fatal(e)
		}
	}

	for _, path := range []string{"/api/llm/profiles", "/api/llm", "/api/mcp", "/api/notify/channels", "/api/tasks/" + task.ID + "/credentials"} {
		resp := request("GET", path, "")
		for _, sec := range secrets {
			if resp.Code != 200 || strings.Contains(resp.Body.String(), sec) {
				t.Fatalf("GET %s: HTTP %d or a secret returned in clear", path, resp.Code)
			}
		}
	}

	// MCP: echo the masked list entry back with a name change; the key survives.
	var list struct{ Servers []db.MCPServer }
	_ = json.Unmarshal(request("GET", "/api/mcp", "").Body.Bytes(), &list)
	for _, m := range list.Servers {
		if m.ID != mcpID {
			continue
		}
		if string(m.Env) != `{"EMPTY":"","X-API-Key":"__masked__"}` {
			t.Fatalf("mcp env not masked as expected: %s", m.Env)
		}
		m.Name = "t-secapi-renamed"
		body, _ := json.Marshal(m)
		if resp := request("POST", "/api/mcp", string(body)); resp.Code != 200 {
			t.Fatalf("mcp save: HTTP %d %s", resp.Code, resp.Body.String())
		}
	}
	all, _ := pg.ListMCP()
	for _, m := range all {
		if m.ID == mcpID && !strings.Contains(string(m.Env), secrets[1]) {
			t.Fatal("masked placeholder replaced the stored MCP secret")
		}
	}
	if resp := request("POST", "/api/mcp", `{"name":"t-secapi-new","transport":"http","env":{"K":"__masked__"}}`); resp.Code != 400 {
		t.Fatalf("a placeholder with nothing stored must be refused, got HTTP %d", resp.Code)
	}

	// Task credential: a masked password on write is refused, the stored one kept.
	if resp := request("POST", "/api/tasks/"+task.ID+"/credentials", `{"domain":"x.example","username":"u","password":"__masked__"}`); resp.Code != 400 {
		t.Fatalf("masked credential write must be refused, got HTTP %d", resp.Code)
	}
	if creds, _ := pg.TaskCredentials(taskID); len(creds) != 1 || creds[0].Password != secrets[2] {
		t.Fatal("stored credential changed")
	}
}
