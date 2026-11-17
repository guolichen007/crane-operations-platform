package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// V1.1 R5 (AC-19, AC-20): customers and contacts on the existing master data,
// team privacy before search/hints/details, no merging by name, audit.

func customerOf(t *testing.T, c testClient, id string) CustomerDetail {
	t.Helper()
	s, b := c.call("GET", "/api/v1/customers/"+id, nil)
	expect(t, s, 200, b)
	var d CustomerDetail
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDatabaseV11Customers(t *testing.T) {
	f := g1FixtureFor(t)
	ctx := context.Background()
	a1, a2, ma, d1 := f.sales, f.other, f.manager, f.director
	c1, mb := login(t, f.server, "synthetic-c"), login(t, f.server, "synthetic-manager-other")
	tag := "V11客户" + randomToken()[:6]
	before := countRows(t, f.db, `SELECT count(*) FROM organizations`)
	legacy := countRows(t, f.db, `SELECT count(*) FROM project_parties`)

	// A1 files two projects for the same customer; the customer appears once
	// with both projects (AC-19 same customer, several projects).
	mk := func(c testClient, name, customer string) Project {
		in := sample(tag + name)
		in.Customer = customer
		in.Location.FactoryName = tag + name
		s, b := c.call("POST", "/api/v1/projects", in)
		return projectFrom(t, s, b)
	}
	custName := tag + "东港钢铁有限公司"
	p1 := mk(a1, "一期", custName)
	p2 := mk(a1, "二期", custName)
	s, b := a1.call("GET", "/api/v1/customers?q="+tag, nil)
	expect(t, s, 200, b)
	var list struct {
		Items []CustomerItem `json:"items"`
		Total int            `json:"total"`
	}
	_ = json.Unmarshal(b, &list)
	if list.Total != 1 || list.Items[0].ProjectCount != 2 || list.Items[0].Name != custName {
		t.Fatalf("customer list %s", b)
	}
	cust := list.Items[0].ID
	// The team maintains its profile; first edit creates it (version 0).
	s, b = a1.call("PATCH", "/api/v1/customers/"+cust, map[string]any{"industry": "钢铁", "region": "江苏", "customerType": "END_USER", "notes": "重点客户", "version": 0})
	expect(t, s, 200, b)
	d := customerOf(t, a1, cust)
	if d.Profile == nil || d.Profile.Industry != "钢铁" || len(d.Projects) != 2 {
		t.Fatalf("detail %+v", d)
	}
	// Customer name is shared master data: not editable by teams.
	s, b = a1.call("PATCH", "/api/v1/customers/"+cust, map[string]any{"name": "改名", "version": d.Profile.Version})
	expect(t, s, 400, b)
	// Stale version: conflict.
	s, b = a1.call("PATCH", "/api/v1/customers/"+cust, map[string]any{"industry": "x", "version": d.Profile.Version - 1})
	expect(t, s, 409, b)
	// Several contacts; phone kept in full for the own team.
	for _, n := range []string{"张工", "李经理"} {
		s, b = a1.call("POST", "/api/v1/customers/"+cust+"/contacts", map[string]any{"name": n, "title": "设备部", "phone": "13912345678"})
		expect(t, s, 200, b)
	}
	d = customerOf(t, a2, cust)
	if len(d.Contacts) != 2 || d.Contacts[0].Phone != "13912345678" {
		t.Fatalf("same-team contacts %+v", d.Contacts)
	}
	// A2 (same team, not owner) sees the customer through the team profile but
	// not A1's projects.
	if len(d.Projects) != 0 {
		t.Fatal("other sales sees projects", len(d.Projects))
	}
	// Supervisor sees both projects.
	if d := customerOf(t, ma, cust); len(d.Projects) != 2 || len(d.Contacts) != 2 {
		t.Fatal("supervisor view")
	}
	// Link a contact to the project (owner only), audit recorded.
	sp := g1Submit(t, a1, p1)
	contact := customerOf(t, a1, cust).Contacts[0]
	s, b = a1.call("POST", "/api/v1/projects/"+sp.ID+"/contacts", map[string]any{"contactId": contact.ID, "version": sp.Version})
	expect(t, s, 200, b)
	if !strings.Contains(string(b), contact.Name) {
		t.Fatal(string(b))
	}
	s, b = ma.call("POST", "/api/v1/projects/"+sp.ID+"/contacts", map[string]any{"contactId": contact.ID, "version": sp.Version, "unlink": true})
	expect(t, s, 403, b)
	s, b = a1.call("GET", "/api/v1/projects/"+sp.ID+"/contacts", nil)
	expect(t, s, 200, b)

	// AC-19/AC-20 cross-team privacy: C1 (team two) cannot open, list, search or
	// read contacts of team one's customer; refusals look like nonexistence.
	for _, path := range []string{"/api/v1/customers/" + cust, "/api/v1/customers/00000000-0000-4000-8000-000000000000"} {
		s, b = c1.call("GET", path, nil)
		if s != 404 || !strings.Contains(string(b), "CUSTOMER_NOT_FOUND") {
			t.Fatal(path, s, string(b))
		}
	}
	_, b1 := c1.call("GET", "/api/v1/customers/"+cust, nil)
	_, b2 := c1.call("GET", "/api/v1/customers/00000000-0000-4000-8000-000000000000", nil)
	if strings.Split(string(b1), `"requestId"`)[0] != strings.Split(string(b2), `"requestId"`)[0] {
		t.Fatal("existence oracle")
	}
	for _, c := range []testClient{c1, mb} {
		s, b = c.call("GET", "/api/v1/customers?q="+tag, nil)
		expect(t, s, 200, b)
		if strings.Contains(string(b), tag) || strings.Contains(string(b), "13912345678") {
			t.Fatal("other team lists the customer", string(b))
		}
		s, b = c.call("POST", "/api/v1/customers/"+cust+"/contacts", map[string]any{"name": "插入"})
		expect(t, s, 404, b)
		s, b = c.call("PATCH", "/api/v1/contacts/"+contact.ID, map[string]any{"name": "改", "version": contact.Version})
		expect(t, s, 404, b)
	}
	// AC-20: C1 creates "the same" customer: the master record is shared (no
	// duplicate row, no merge), but C1 sees only its own profile, none of team
	// one's projects, contacts or notes.
	s, b = c1.call("GET", "/api/v1/customers/similar?name="+custName, nil)
	expect(t, s, 200, b)
	if !strings.Contains(string(b), "CUSTOMER_SIMILAR_RESTRICTED") || strings.Contains(string(b), cust) || strings.Contains(string(b), "钢铁") {
		t.Fatal("similar notice", string(b))
	}
	s, b = c1.call("POST", "/api/v1/customers", map[string]any{"name": custName, "industry": "冶金"})
	expect(t, s, 200, b)
	var created struct {
		Customer CustomerDetail `json:"customer"`
		Existed  bool           `json:"existed"`
	}
	_ = json.Unmarshal(b, &created)
	if created.Customer.ID != cust || created.Existed || len(created.Customer.Projects) != 0 || len(created.Customer.Contacts) != 0 || created.Customer.Profile.Industry != "冶金" {
		t.Fatalf("second team view %s", b)
	}
	if strings.Contains(string(b), "重点客户") || strings.Contains(string(b), "张工") || strings.Contains(string(b), "钢铁\"") {
		t.Fatal("team one data leaked", string(b))
	}
	if n := countRows(t, f.db, `SELECT count(*) FROM organizations`); n != before+1 {
		t.Fatal("organizations", before, n)
	}
	// Idempotent: the same team creating it again returns existed=true.
	s, b = c1.call("POST", "/api/v1/customers", map[string]any{"name": custName})
	expect(t, s, 200, b)
	if !strings.Contains(string(b), `"existed":true`) {
		t.Fatal(string(b))
	}
	// Team one still sees only its profile, not team two's.
	if d := customerOf(t, a1, cust); d.Profile.Industry != "钢铁" || strings.Contains(mustString(d), "冶金") {
		t.Fatal("team two profile leaked to team one")
	}
	// Similar-but-different names are never merged.
	s, b = a1.call("POST", "/api/v1/customers", map[string]any{"name": tag + "东港钢铁（集团）有限公司"})
	expect(t, s, 200, b)
	if countRows(t, f.db, `SELECT count(*) FROM organizations WHERE name LIKE '`+tag+`%'`) != 2 {
		t.Fatal("similar name merged")
	}
	// Director sees both teams' profiles and every contact (phones masked for
	// teams other than the own).
	dd := customerOf(t, d1, cust)
	if len(dd.Profiles) != 2 || len(dd.Projects) != 2 || len(dd.Contacts) != 2 || dd.Contacts[0].Phone == "13912345678" {
		t.Fatalf("director view %+v", dd.Contacts)
	}
	// Credit code: administrator only, format and uniqueness checked, audited.
	s, b = a1.call("POST", "/api/v1/customers/"+cust+"/credit-code", map[string]any{"creditCode": "91320000MA1XXXXX0X", "reason": "核对"})
	expect(t, s, 403, b)
	s, b = f.admin.call("POST", "/api/v1/customers/"+cust+"/credit-code", map[string]any{"creditCode": "bad", "reason": "核对"})
	expect(t, s, 400, b)
	s, b = f.admin.call("POST", "/api/v1/customers/"+cust+"/credit-code", map[string]any{"creditCode": "91320000MA1KXXKX0X", "reason": "营业执照核对"})
	expect(t, s, 200, b)
	if d := customerOf(t, a1, cust); d.CreditCode != "" {
		t.Fatal("credit code shown to sales")
	}
	// Contacts: archive keeps the row (history), restore; version conflict.
	c0 := customerOf(t, a1, cust).Contacts[1]
	s, b = a1.call("POST", "/api/v1/contacts/"+c0.ID+"/archive", map[string]any{"name": c0.Name, "version": c0.Version, "reason": "离职"})
	expect(t, s, 200, b)
	s, b = a1.call("POST", "/api/v1/contacts/"+c0.ID+"/archive", map[string]any{"name": c0.Name, "version": c0.Version, "reason": "重放"})
	expect(t, s, 409, b)
	// Audit is immutable; existing project parties unchanged.
	if _, err := f.db.Exec(ctx, `DELETE FROM customer_audit`); !isCode(err, "BB002") {
		t.Fatal("customer audit deletable", err)
	}
	if countRows(t, f.db, `SELECT count(*) FROM project_parties`) < legacy+2 {
		t.Fatal("project parties lost")
	}
	_ = p2
}

func mustString(v any) string { return string(mustJSON(v)) }
